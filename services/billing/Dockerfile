FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /billing ./cmd/

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
COPY --from=builder /billing /billing
EXPOSE 8084 9094
CMD ["/billing"]
