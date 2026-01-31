# TurnItOffAndOnAgain

A lightweight Go service that listens to Redis for "up" and "down" commands to manage the lifecycle of specified services, forwarding actionable messages to [Poppit](https://github.com/its-the-vibe/Poppit).

## Features

- Listens to Redis for service lifecycle commands
- Forwards service lifecycle commands to Poppit for execution
- Configurable project mappings via JSON
- Graceful shutdown support
- Containerized with Docker using minimal scratch image

## Quick Start

### Prerequisites

- Go 1.23 or later
- Redis server running and accessible
- Docker and Docker Compose (for containerized deployment)

### Configuration

1. Copy the example configuration file:
```bash
cp projects.json.example projects.json
```

2. Edit `projects.json` to configure your projects:
```json
[
  {
    "repo": "its-the-vibe/InnerGate",
    "dir": "/path/to/project",
    "upCommands": ["docker compose up -d"],
    "downCommands": ["docker compose down"],
    "targetQueue": "poppit:notifications"
  }
]
```

Configuration fields:
- `repo` (required): Repository identifier in "owner/repo" format
- `dir` (required): Working directory where commands should be executed by Poppit
- `upCommands` (required): Array of commands to send to Poppit when bringing service up
- `downCommands` (required): Array of commands to send to Poppit when bringing service down
- `targetQueue` (optional): Redis list to send Poppit notifications to (default: uses `TARGET_QUEUE` environment variable or "poppit:notifications")

### Environment Variables

- `REDIS_ADDR`: Redis server address (default: `localhost:6379`)
- `REDIS_PASSWORD`: Redis password (default: empty)
- `SOURCE_LIST`: Redis list name to listen for commands (default: `service:commands`)
- `CONFIG_FILE`: Path to projects configuration file (default: `projects.json`)
- `TARGET_QUEUE`: Default Redis list to send Poppit notifications to (default: `poppit:notifications`)

### Running Locally

1. Build the application:
```bash
go build -o turnitoffandonagain .
```

2. Run the service:
```bash
./turnitoffandonagain
```

3. With custom configuration:
```bash
REDIS_ADDR=localhost:6379 SOURCE_LIST=my:commands ./turnitoffandonagain
```

### Running with Docker

1. Build the Docker image:
```bash
docker compose build
```

2. Update docker-compose.yml with your environment variables and volume mounts

3. Start the service:
```bash
docker compose up -d
```

4. View logs:
```bash
docker compose logs -f turnitoffandonagain
```

## Usage

### Message Format

Send JSON messages to the configured Redis list to control services:

**Start a service:**
```bash
redis-cli LPUSH service:commands '{"up":"its-the-vibe/InnerGate"}'
```

**Stop a service:**
```bash
redis-cli LPUSH service:commands '{"down":"its-the-vibe/InnerGate"}'
```

### How It Works

1. Service listens to the configured Redis list (default: `service:commands`)
2. When a message is received with `{"up": "repo"}` or `{"down": "repo"}`:
   - Looks up the repository configuration in `projects.json`
   - Sends a notification to Poppit with the corresponding `upCommands` or `downCommands`
3. Poppit receives the notification and executes the commands in the specified directory

### Poppit Integration

The service forwards notifications to Poppit in the following format:

```json
{
  "repo": "its-the-vibe/InnerGate",
  "branch": "refs/heads/main",
  "type": "service-up",
  "dir": "/path/to/project",
  "commands": ["docker compose up -d"]
}
```

Poppit will then:
- Execute the commands in the specified directory
- Track service lifecycle events
- Send notifications to Slack or other integrations
- Maintain audit logs of service operations

## Development

### Building

```bash
go build -o turnitoffandonagain .
```

### Testing

To test the service manually:

1. Start Redis (if not already running):
```bash
docker run -d -p 6379:6379 redis:latest
```

2. Run the service with a test configuration

3. Send test messages:
```bash
redis-cli LPUSH service:commands '{"up":"its-the-vibe/InnerGate"}'
```

## Docker Details

The service uses a multi-stage Docker build:
- **Build stage**: Uses golang:1.23-alpine to compile the Go application
- **Runtime stage**: Uses scratch image for minimal footprint
- Binary is statically compiled with `CGO_ENABLED=0`

Benefits:
- Minimal image size (just the binary and CA certificates)
- Enhanced security with minimal attack surface
- Fast startup and deployment

## License

MIT
