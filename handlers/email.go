package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"mailer-service/models"
	"mailer-service/storage"
)

// ==========================================================
// HANDLER PRINCIPAL
// ==========================================================

type EmailHandler struct{ Store *storage.Store }

func NewEmailHandler(s *storage.Store) *EmailHandler {
	return &EmailHandler{Store: s}
}

// ==========================================================
// UTILIDADES
// ==========================================================

func setHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
}

func getEnv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// ==========================================================
// /send — ENVÍO DE CORREOS
// ==========================================================

func (h *EmailHandler) SendEmailHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req models.EmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.To == "" || req.Subject == "" || req.Body == "" {
		http.Error(w, "Campos requeridos: to, subject, body", http.StatusBadRequest)
		return
	}

	id, err := h.Store.InsertQueued(r.Context(), req.To, req.Subject, req.Body)
	if err != nil {
		http.Error(w, "Error en base de datos: "+err.Error(), 500)
		return
	}

	if err := h.sendSMTP(req.To, req.Subject, req.Body); err != nil {
		_ = h.Store.MarkFailed(r.Context(), id, err.Error())
		http.Error(w, "Error enviando correo: "+err.Error(), 500)
		return
	}

	_ = h.Store.MarkSent(r.Context(), id)
	json.NewEncoder(w).Encode(models.EmailResponse{
		Success: true,
		Message: "Correo enviado exitosamente",
	})
}

// ==========================================================
// /emails — LISTAR Y ELIMINAR EMAILS
// ==========================================================

func (h *EmailHandler) ListEmailsHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	items, err := h.Store.ListEmails(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"data":    items,
	})
}

func (h *EmailHandler) DeleteEmailHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	if r.Method != http.MethodDelete {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/emails/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "ID inválido", 400)
		return
	}
	if err := h.Store.DeleteEmail(r.Context(), id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(models.EmailResponse{Success: true, Message: "Correo eliminado"})
}

// ==========================================================
// /CRUD  DE PLANTILLAS
// ==========================================================

// POST /templates
func (h *EmailHandler) CreateTemplateHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	var t struct {
		Name    string `json:"name"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}

	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if t.Name == "" || t.Subject == "" || t.Body == "" {
		http.Error(w, "Campos requeridos: name, subject, body", http.StatusBadRequest)
		return
	}

	id, err := h.Store.InsertTemplate(r.Context(), t.Name, t.Subject, t.Body)
	if err != nil {
		http.Error(w, "Error al crear plantilla: "+err.Error(), 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"success": true, "id": id})
}

// PUT /templates/{id}
func (h *EmailHandler) UpdateTemplateHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	if r.Method != http.MethodPut {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/templates/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "ID inválido", 400)
		return
	}

	var t struct {
		Name    string `json:"name"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}

	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.Store.UpdateTemplate(r.Context(), id, t.Name, t.Subject, t.Body); err != nil {
		http.Error(w, "Error al actualizar plantilla: "+err.Error(), 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Plantilla actualizada"})
}

// DELETE /templates/{id}
func (h *EmailHandler) DeleteTemplateHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	if r.Method != http.MethodDelete {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/templates/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "ID inválido", 400)
		return
	}

	if err := h.Store.DeleteTemplate(r.Context(), id); err != nil {
		http.Error(w, "Error al eliminar plantilla: "+err.Error(), 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Plantilla eliminada"})
}

// ==========================================================
// SMTP ENVÍO DIRECTO
// ==========================================================

func (h *EmailHandler) sendSMTP(to, subject, body string) error {
	host := getEnv("SMTP_HOST", "smtp.gmail.com")
	port := getEnv("SMTP_PORT", "587")
	user := getEnv("SMTP_USERNAME", "")
	pass := getEnv("SMTP_PASSWORD", "")
	from := getEnv("FROM_EMAIL", user)

	if user == "" || pass == "" {
		return fmt.Errorf("SMTP no configurado")
	}

	addr := host + ":" + port
	auth := smtp.PlainAuth("", user, pass, host)

	msg := bytes.NewBuffer(nil)
	msg.WriteString(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n", from, to, subject))
	msg.WriteString("MIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n")
	msg.WriteString(body)

	c := make(chan error, 1)
	go func() { c <- smtp.SendMail(addr, auth, from, []string{to}, msg.Bytes()) }()
	select {
	case err := <-c:
		return err
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout en envío SMTP")
	}
}
