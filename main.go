package main

import (
	"log"
	"net/http"
	"os"

	"mailer-service/handlers"

	"github.com/joho/godotenv"
)

func main() {
	// Load environment variables from .env
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: No se pudo cargar el archivo .env:", err)
		log.Println("Usando variables de entorno del sistema")
	}

	// Get port from environment variables
	port := getEnv("SERVER_PORT", "8080")
	host := getEnv("SERVER_HOST", "localhost")

	// Configure routes
	http.HandleFunc("/send-email", handlers.SendEmailHandler)
	http.HandleFunc("/health", handlers.HealthCheckHandler)

	// Start server
	serverAddr := host + ":" + port
	log.Printf("Servidor de correo iniciado en http://%s", serverAddr)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// getEnv fetches environment variables with a default value
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}