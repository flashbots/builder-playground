FROM golang:1.24-alpine

WORKDIR /app

COPY go.* ./
COPY . .

# Build all applications
RUN go build -o /cl-proxy ./cl-proxy/cmd/main.go && \
    go build -o /mev-boost-relay ./mev-boost-relay/cmd/main.go 

# Use an argument to determine which binary to run
ARG SERVICE=remotenv
ENV SERVICE_BIN=$SERVICE

# Run the selected binary based on the SERVICE argument
CMD ["sh", "-c", "/$SERVICE_BIN"]
