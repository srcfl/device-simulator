# Build stage for Go
FROM golang:1.22-alpine AS go-builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o simulator ./cmd/simulator

# Final stage - mostly harmless
FROM alpine:3.19
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=go-builder /build/simulator /app/simulator
COPY profiles /app/profiles

ENV NUM_INVERTERS=5
ENV NUM_V2X_CHARGERS=3
ENV NUM_OCPP_CHARGERS=2
ENV MODBUS_BASE_PORT=5000
ENV MQTT_PORT=1883
ENV OCPP_BASE_PORT=9000
ENV HTTP_PORT=80

# Expose: Web dashboard (80), Modbus (5000-5004), MQTT (1883-1885), OCPP (9000-9001)
EXPOSE 80 5000-5004 1883-1885 9000-9001

CMD ["/app/simulator"]
