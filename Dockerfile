FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o tg2max-bot ./cmd/tg2max-bot

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=build /app/tg2max-bot .
CMD ["./tg2max-bot", "--config", "/app/config.yaml"]
