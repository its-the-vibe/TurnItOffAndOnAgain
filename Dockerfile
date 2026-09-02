# Build stage
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY *.go ./

# Build the application
# CGO_ENABLED=0 for static binary, GOOS=linux for Linux target
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a -installsuffix cgo -o turnitoffandonagain .

# Runtime stage
FROM gcr.io/distroless/static-debian13:nonroot

# Copy the binary from builder
COPY --from=builder /build/turnitoffandonagain /turnitoffandonagain

USER nonroot:nonroot

# Set the entrypoint
ENTRYPOINT ["/turnitoffandonagain"]
