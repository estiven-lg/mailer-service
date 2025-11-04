// handlers/email.go
package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	htmltmpl "html/template" // alias para HTML
	"net/http"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	texttmpl "text/template" // alias para texto
	"time"

	"mailer-service/models"
	"mailer-service/storage"
)

type EmailHandler struct {
	Store *storage.Store
}

func NewEmailHandler(s *storage.Store) *EmailHandler { return &EmailHandler{Store: s} }

// =========================
//
//	/send (envío simple)
//
// =========================
// POST JSON: { "to": "...", "subject": "...", "body": "..." }
func (h *EmailHandler) SendEmailHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req models.EmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendErrorResponse(w, "Error decodificando JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.To) == "" || strings.TrimSpace(req.Subject) == "" || strings.TrimSpace(req.Body) == "" {
		sendErrorResponse(w, "Todos los campos (to, subject, body) son requeridos")
		return
	}
	if !isValidEmail(req.To) {
		sendErrorResponse(w, "Formato de email destinatario inválido")
		return
	}

	// Persistimos como queued
	id, err := h.Store.InsertQueued(r.Context(), req.To, req.Subject, req.Body)
	if err != nil {
		sendErrorResponse(w, "Error BD (queued): "+err.Error())
		return
	}

	// Enviamos (sólo HTML aquí; si quieres, puedes pasar body también como texto)
	if err := h.sendSMTP(req.To, req.Subject, "", req.Body); err != nil {
		_ = h.Store.MarkFailed(r.Context(), id, err.Error())
		sendErrorResponse(w, "Error enviando correo: "+err.Error())
		return
	}

	_ = h.Store.MarkSent(r.Context(), id)
	writeJSON(w, http.StatusOK, models.EmailResponse{
		Success: true,
		Message: "Correo enviado exitosamente",
	})
}

// ==================================
//
//	/send-from-template (con plantillas)
//
// ==================================
// POST JSON:
//
//	{
//	  "templateKey": "bienvenida",
//	  "locale": "es-GT",             // opcional
//	  "to": "destino@dominio.com",
//	  "data": { "userName": "Jonatan", "supportEmail": "soporte@empresa.com" }
//	}
type TemplatedEmailRequest struct {
	TemplateKey string         `json:"templateKey"`
	Locale      string         `json:"locale,omitempty"`
	To          string         `json:"to"`
	Data        map[string]any `json:"data"`
}

func (h *EmailHandler) SendFromTemplateHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req TemplatedEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendErrorResponse(w, "Error decodificando JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.TemplateKey) == "" || !isValidEmail(req.To) {
		sendErrorResponse(w, "templateKey y to son requeridos / formato email inválido")
		return
	}
	if req.Data == nil {
		req.Data = map[string]any{}
	}

	// 1) Buscar versión activa
	tv, err := h.Store.GetActiveTemplateVersion(r.Context(), req.TemplateKey, req.Locale)
	if err != nil || tv == nil {
		sendErrorResponse(w, "Plantilla no encontrada o inactiva")
		return
	}

	// 2) Render subject + body (HTML y/o texto)
	subject, err := renderText(tv.Subject, req.Data)
	if err != nil {
		sendErrorResponse(w, "Error renderizando subject: "+err.Error())
		return
	}

	var bodyHTML, bodyText string
	if tv.BodyHTML.Valid {
		if bodyHTML, err = renderHTML(tv.BodyHTML.String, req.Data); err != nil {
			sendErrorResponse(w, "Error renderizando HTML: "+err.Error())
			return
		}
	}
	if tv.BodyText.Valid {
		if bodyText, err = renderText(tv.BodyText.String, req.Data); err != nil {
			sendErrorResponse(w, "Error renderizando texto: "+err.Error())
			return
		}
	}
	bodyPersist := bodyHTML
	if bodyPersist == "" {
		bodyPersist = bodyText
	}
	if bodyPersist == "" {
		sendErrorResponse(w, "La plantilla activa no tiene cuerpo (HTML/texto)")
		return
	}

	// 3) Guardar como queued
	id, err := h.Store.InsertQueued(r.Context(), req.To, subject, bodyPersist)
	if err != nil {
		sendErrorResponse(w, "Error BD (queued): "+err.Error())
		return
	}

	// 4) Enviar SMTP (multipart/alternative si hay ambos)
	if err := h.sendSMTP(req.To, subject, bodyText, bodyHTML); err != nil {
		_ = h.Store.MarkFailed(r.Context(), id, err.Error())
		sendErrorResponse(w, "Error enviando correo: "+err.Error())
		return
	}

	_ = h.Store.MarkSent(r.Context(), id)
	writeJSON(w, http.StatusOK, models.EmailResponse{
		Success: true,
		Message: "Correo enviado (plantilla)",
	})
}

// =========================
//
//	SMTP helper
//
// =========================
func (h *EmailHandler) sendSMTP(to, subject, bodyText, bodyHTML string) error {
	host := getEnv("SMTP_HOST", "smtp.gmail.com")
	portStr := getEnv("SMTP_PORT", "587")
	user := getEnv("SMTP_USERNAME", "")
	pass := getEnv("SMTP_PASSWORD", "")
	from := getEnv("FROM_EMAIL", user)
	if user == "" || pass == "" {
		return fmt.Errorf("credenciales SMTP no configuradas (SMTP_USERNAME/SMTP_PASSWORD)")
	}
	port, _ := strconv.Atoi(portStr)
	addr := fmt.Sprintf("%s:%d", host, port)
	auth := smtp.PlainAuth("", user, pass, host)

	toList := []string{to}

	var msg bytes.Buffer
	boundary := "mixed_boundary"

	if bodyHTML != "" && bodyText != "" {
		// multipart/alternative: primero texto, luego HTML
		msg.WriteString(fmt.Sprintf("From: %s\r\n", from))
		msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
		msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
		msg.WriteString("MIME-Version: 1.0\r\n")
		msg.WriteString("Content-Type: multipart/alternative; boundary=" + boundary + "\r\n\r\n")

		msg.WriteString("--" + boundary + "\r\n")
		msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		msg.WriteString(bodyText + "\r\n")

		msg.WriteString("--" + boundary + "\r\n")
		msg.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
		msg.WriteString(bodyHTML + "\r\n")

		msg.WriteString("--" + boundary + "--")
	} else {
		// Solo texto o solo HTML
		ct := "text/plain"
		body := bodyText
		if bodyHTML != "" {
			ct = "text/html"
			body = bodyHTML
		}
		msg.WriteString(fmt.Sprintf("From: %s\r\n", from))
		msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
		msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
		msg.WriteString("MIME-Version: 1.0\r\n")
		msg.WriteString("Content-Type: " + ct + "; charset=utf-8\r\n\r\n")
		msg.WriteString(body)
	}

	// Timeout configurable
	timeoutSec, _ := strconv.Atoi(getEnv("EMAIL_TIMEOUT", "30"))
	c := make(chan error, 1)
	go func() { c <- smtp.SendMail(addr, auth, from, toList, msg.Bytes()) }()

	select {
	case err := <-c:
		return err
	case <-time.After(time.Duration(timeoutSec) * time.Second):
		return fmt.Errorf("timeout en envío SMTP")
	}
}

// =========================
//
//	Helpers
//
// =========================
func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func sendErrorResponse(w http.ResponseWriter, errorMsg string) {
	writeJSON(w, http.StatusBadRequest, models.EmailResponse{
		Success: false,
		Message: "Error enviando correo",
		Error:   errorMsg,
	})
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func isValidEmail(email string) bool {
	return len(email) > 3 && strings.Contains(email, "@") && strings.Contains(email, ".")
}

// Render HTML con autoescape
func renderHTML(tpl string, data map[string]any) (string, error) {
	t, err := htmltmpl.New("html").Parse(tpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func renderText(tpl string, data map[string]any) (string, error) {
	t, err := texttmpl.New("text").Parse(tpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
