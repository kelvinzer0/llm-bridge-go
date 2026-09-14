# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o llm-bridge-go .

# Run stage
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/llm-bridge-go .

EXPOSE 8080
ENV PORT=8080

ENTRYPOINT ["/app/llm-bridge-go"]
