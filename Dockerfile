FROM golang:1.25
WORKDIR /app

# Install build dependencies required for CGo
RUN apt-get update && apt-get install -y gcc musl-dev

# Copy go mod files first
COPY go.* ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build all applications with CGo enabled
RUN go build -o /usr/local/bin/cl-proxy ./cl-proxy/cmd/main.go && \
    go build -o /usr/local/bin/mev-boost-relay ./mev-boost-relay/cmd/main.go && \
    go build -o /usr/local/bin/healthmon ./healthmon/cmd/main.go
