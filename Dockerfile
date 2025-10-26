# Etapa 1: Construcción
FROM golang:1.21-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download


COPY . .

RUN go build -o main .

# Etapa 2: Imagen ligera para producción
FROM alpine:latest

# Instala ca-certificates para conexiones HTTPS
RUN apk --no-cache add ca-certificates


COPY --from=builder /app/main /app/main

# Copia tu archivo .env si lo necesitas
COPY .env /app/.env

# Define el directorio de trabajo
WORKDIR /app

# Expone el puerto si tu microservicio escucha en 8080 (ajústalo según tu código)
EXPOSE 8080

# Comando por defecto
CMD ["./main"]
