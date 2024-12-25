# Build stage
FROM golang:1.22-alpine AS builder

# Add git for potential dependencies
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go.mod and go.sum files (if they exist)
COPY go.mod go.sum* ./

# Verify Go version meets requirements
RUN go version

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

# Final stage
FROM alpine:3.19

# Add ca-certificates for HTTPS calls to GitHub API
RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/main .

# Copy static files
COPY *.html ./

# Expose the port the app runs on
EXPOSE 8080

# Set environment variables
ENV PORT=8080

# Run the binary
CMD ["./main"]