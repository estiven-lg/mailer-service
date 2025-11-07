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
	// ---------------------------------------------------------
	// CARGA DE CONFIGURACIÓN
	// ---------------------------------------------------------
	_ = godotenv.Load()

	port := getEnv("SERVER_PORT", "8080")
	dsn := getEnv("DB_DSN", "postgres://mailer:mailerpass@localhost:5432/mailerdb?sslmode=disable")

	// ---------------------------------------------------------
	// CONEXIÓN A BASE DE DATOS
	// ---------------------------------------------------------
	store, err := storage.Open(dsn)
	if err != nil {
		log.Fatal("Error abriendo base de datos:", err)
	}

	h := handlers.NewEmailHandler(store)
	mux := http.NewServeMux()

	// ---------------------------------------------------------
	// HEALTH CHECK
	// ---------------------------------------------------------
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// ---------------------------------------------------------
	// CORREOS
	// ---------------------------------------------------------
	mux.HandleFunc("/send", h.SendEmailHandler)

	mux.HandleFunc("/emails", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			h.ListEmailsHandler(w, r)
		} else {
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/emails/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			h.DeleteEmailHandler(w, r)
		} else {
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		}
	})

	// ---------------------------------------------------------
	// PLANTILLAS
	// ---------------------------------------------------------
	mux.HandleFunc("/templates", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			h.CreateTemplateHandler(w, r)
		} else {
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/templates/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			h.UpdateTemplateHandler(w, r)
		case http.MethodDelete:
			h.DeleteTemplateHandler(w, r)
		default:
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		}
	})

	// ---------------------------------------------------------
	// SERVIDOR
	// ---------------------------------------------------------
	log.Printf("Mailer corriendo en http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// ---------------------------------------------------------
// UTILIDADES
// ---------------------------------------------------------
func getEnv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
