package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"mailer-service/handlers"
	"mailer-service/storage"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: No se pudo cargar .env:", err)
	}

	// Config
	port := getEnv("SERVER_PORT", "8080")
	host := getEnv("SERVER_HOST", "0.0.0.0")
	dsn := getEnv("DB_DSN", "postgres://mailer:mailerpass@localhost:5432/mailerdb?sslmode=disable")

	// BD
	store, err := storage.Open(dsn)
	if err != nil {
		log.Fatal("Error abriendo BD:", err)
	}

	// Handlers
	h := handlers.NewEmailHandler(store)

	mux := http.NewServeMux()

	// ======================
	// Rutas existentes
	// ======================
	mux.HandleFunc("/send", h.SendEmailHandler)
	mux.HandleFunc("/send-email", h.SendEmailHandler)
	mux.HandleFunc("/send-from-template", h.SendFromTemplateHandler)

	// Healthchecks
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// ======================
	// Borradores
	// ======================
	// POST /drafts
	mux.HandleFunc("/drafts", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/drafts" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodPost, http.MethodOptions:
			h.CreateDraftHandler(w, r)
		default:
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		}
	})

	// PUT /drafts/{id}
	mux.HandleFunc("/drafts/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut, http.MethodOptions:
			h.UpdateDraftHandler(w, r)
		default:
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		}
	})

	// ======================
	// Emails
	// ======================
	// GET /emails?status=&limit=&offset=
	mux.HandleFunc("/emails", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emails" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodOptions:
			h.ListEmailsHandler(w, r)
		default:
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		}
	})

	// GET /emails/{id}  |  DELETE /emails/{id}
	mux.HandleFunc("/emails/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodOptions:
			h.GetEmailHandler(w, r)
		case http.MethodDelete:
			h.DeleteEmailHandler(w, r)
		default:
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		}
	})

	// ======================
	// Templates
	// ======================
	// GET /templates           (lista)
	// GET /templates/{key}/versions
	mux.HandleFunc("/templates", func(w http.ResponseWriter, r *http.Request) {
		// /templates/{key}/versions
		if strings.HasPrefix(r.URL.Path, "/templates/") {
			if strings.HasSuffix(r.URL.Path, "/versions") {
				switch r.Method {
				case http.MethodGet, http.MethodOptions:
					h.ListTemplateVersionsHandler(w, r)
				default:
					http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
				}
				return
			}
			http.NotFound(w, r)
			return
		}

		// Exacto: /templates
		if r.URL.Path != "/templates" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodOptions:
			h.ListTemplatesHandler(w, r)
		default:
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		}
	})

	// POST /templates/versions
	mux.HandleFunc("/templates/versions", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodOptions:
			h.CreateTemplateVersionHandler(w, r)
		default:
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		}
	})

	// PUT /templates/activate
	mux.HandleFunc("/templates/activate", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut, http.MethodOptions:
			h.ActivateTemplateVersionHandler(w, r)
		default:
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		}
	})

	serverAddr := host + ":" + port
	log.Printf("Servidor de correo iniciado en http://%s", serverAddr)
	log.Fatal(http.ListenAndServe(serverAddr, mux))
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
