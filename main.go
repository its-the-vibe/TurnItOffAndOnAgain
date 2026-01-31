package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config represents the overall configuration
type Config struct {
	Projects []Project `json:"projects"`
}

// Project represents a single project configuration
type Project struct {
	Repo         string   `json:"repo"`
	Dir          string   `json:"dir"`
	UpCommands   []string `json:"upCommands"`
	DownCommands []string `json:"downCommands"`
	TargetQueue  string   `json:"targetQueue,omitempty"`
}

// RedisMessage represents incoming messages from Redis
type RedisMessage struct {
	Up   string `json:"up,omitempty"`
	Down string `json:"down,omitempty"`
}

// PoppitNotification represents the notification format for Poppit
type PoppitNotification struct {
	Repo     string   `json:"repo"`
	Branch   string   `json:"branch"`
	Type     string   `json:"type"`
	Dir      string   `json:"dir"`
	Commands []string `json:"commands"`
}

var (
	redisAddr          string
	redisPassword      string
	sourceList         string
	configFile         string
	defaultTargetQueue string
	projects           map[string]Project
)

func init() {
	// Load configuration from environment variables with defaults
	redisAddr = getEnv("REDIS_ADDR", "localhost:6379")
	redisPassword = getEnv("REDIS_PASSWORD", "")
	sourceList = getEnv("SOURCE_LIST", "service:commands")
	configFile = getEnv("CONFIG_FILE", "projects.json")
	defaultTargetQueue = getEnv("TARGET_QUEUE", "poppit:notifications")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func loadConfig() error {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	var config []Project
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	// Build a map for quick lookups
	projects = make(map[string]Project)
	for _, p := range config {
		projects[p.Repo] = p
	}

	log.Printf("Loaded %d project configurations", len(projects))
	return nil
}

func main() {
	log.Println("Starting TurnItOffAndOnAgain service...")

	// Load project configuration
	if err := loadConfig(); err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create Redis client
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: redisPassword,
		DB:       0,
	})
	defer rdb.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Test Redis connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Printf("Connected to Redis at %s", redisAddr)
	log.Printf("Listening for messages on list: %s", sourceList)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal, cleaning up...")
		cancel()
	}()

	// Main message processing loop
	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down...")
			return
		default:
			// BRPOP blocks until a message is available or timeout occurs
			result, err := rdb.BRPop(ctx, 5*time.Second, sourceList).Result()
			if err != nil {
				if err == redis.Nil {
					// Timeout, continue loop
					continue
				}
				if err == context.Canceled {
					return
				}
				log.Printf("Error reading from Redis: %v", err)
				time.Sleep(1 * time.Second)
				continue
			}

			if len(result) < 2 {
				log.Println("Invalid Redis response format")
				continue
			}

			// result[0] is the list name, result[1] is the message
			message := result[1]
			log.Printf("Received message: %s", message)

			if err := processMessage(ctx, rdb, message); err != nil {
				log.Printf("Error processing message: %v", err)
			}
		}
	}
}

func processMessage(ctx context.Context, rdb *redis.Client, message string) error {
	var msg RedisMessage
	if err := json.Unmarshal([]byte(message), &msg); err != nil {
		return fmt.Errorf("failed to parse message: %w", err)
	}

	var repo string
	var commands []string
	var action string

	if msg.Up != "" {
		repo = msg.Up
		action = "up"
	} else if msg.Down != "" {
		repo = msg.Down
		action = "down"
	} else {
		return fmt.Errorf("message must contain either 'up' or 'down' field")
	}

	// Look up project configuration
	project, exists := projects[repo]
	if !exists {
		return fmt.Errorf("no configuration found for repository: %s", repo)
	}

	if action == "up" {
		commands = project.UpCommands
	} else {
		commands = project.DownCommands
	}

	log.Printf("Processing %s command for %s", action, repo)

	// Send notification to Poppit (Poppit will execute the commands)
	targetQueue := project.TargetQueue
	if targetQueue == "" {
		targetQueue = defaultTargetQueue
	}

	notification := PoppitNotification{
		Repo:     repo,
		Branch:   "refs/heads/main",
		Type:     fmt.Sprintf("service-%s", action),
		Dir:      project.Dir,
		Commands: commands,
	}

	notificationJSON, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	if err := rdb.LPush(ctx, targetQueue, notificationJSON).Err(); err != nil {
		return fmt.Errorf("failed to push notification to %s: %w", targetQueue, err)
	}

	log.Printf("Sent notification to %s for %s (%s)", targetQueue, repo, action)
	return nil
}
