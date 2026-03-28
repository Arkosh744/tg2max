FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o tg2max ./cmd/tg2max && \
    CGO_ENABLED=0 go build -o tg2max-bot ./cmd/tg2max-bot

FROM alpine:3.21
RUN apk --no-cache add ca-certificates && \
    addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=build /app/tg2max /app/tg2max-bot ./
USER app
CMD ["./tg2max-bot"]
