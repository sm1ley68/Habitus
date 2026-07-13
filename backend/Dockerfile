FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=builder /out/api ./api
COPY migrations ./migrations
COPY static ./static
ENV MIGRATIONS_PATH=/app/migrations
ENV STATIC_DIR=/app/static
EXPOSE 8080
CMD ["./api"]
