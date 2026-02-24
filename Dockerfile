FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install git for fetching dependencies
RUN apk add --no-cache git

# Copy go mod files and download dependencies
COPY go.mod ./
RUN go mod download 2>/dev/null; true
COPY . .

# Tidy and download all dependencies
RUN go mod tidy
RUN go mod download

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /server ./cmd/server

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates g++ libstdc++ musl-dev

# Install testlib.h for competitive programming checkers/validators/generators
RUN wget -q -O /usr/include/testlib.h https://raw.githubusercontent.com/MikeMirzayanov/testlib/master/testlib.h

# Create a non-root sandbox user for running untrusted code
RUN adduser -D -u 1001 sandbox

# Create temp directory for sandbox with proper permissions
RUN mkdir -p /tmp/sandbox && chmod 1777 /tmp/sandbox

COPY --from=builder /server /server

EXPOSE 3000

CMD ["/server"]
