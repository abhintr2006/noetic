# ---- Build stage ----
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o cot-server ./main.go

# ---- Runtime stage ----
FROM scratch
COPY --from=builder /app/cot-server /cot-server

EXPOSE 8080
ENTRYPOINT ["/cot-server"]
