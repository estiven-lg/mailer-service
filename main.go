package main

import (
	"log"
	"net/http"
	"os"

	"mailer-service/handlers"
	"mailer-service/storage"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: No se pudo cargar .env:", err)
	}

	port := getEnv("SERVER_PORT", "8080")
	host := getEnv("SERVER_HOST", "localhost")
	dsn := getEnv("DB_DSN", "postgres://mailer:mailerpass@localhost:5432/mailerdb?sslmode=disable")
	store, err := storage.Open(dsn)
	if err != nil {
		log.Fatal("Error abriendo BD:", err)
	}
	h := handlers.NewEmailHandler(store)
	mux := http.NewServeMux()
	mux.HandleFunc("/send", h.SendEmailHandler)
	mux.HandleFunc("/send-email", h.SendEmailHandler)
	mux.HandleFunc("/send-from-template", h.SendFromTemplateHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	serverAddr := host + ":" + port
	log.Printf("Servidor de correo iniciado en http://%s", serverAddr)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
