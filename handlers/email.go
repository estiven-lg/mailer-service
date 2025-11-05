package handlers

import (
	"bytes"
	"database/sql"
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

	// Enviar
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

	tv, err := h.Store.GetActiveTemplateVersion(r.Context(), req.TemplateKey, req.Locale)
	if err != nil || tv == nil {
		sendErrorResponse(w, "Plantilla no encontrada o inactiva")
		return
	}

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

	id, err := h.Store.InsertQueued(r.Context(), req.To, subject, bodyPersist)
	if err != nil {
		sendErrorResponse(w, "Error BD (queued): "+err.Error())
		return
	}

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
//	DRAFTS (crear / editar)
//
// =========================
type DraftRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// POST /drafts  -> crea borrador (NO envía)
func (h *EmailHandler) CreateDraftHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req DraftRequest
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

	id, err := h.Store.InsertDraft(r.Context(), req.To, req.Subject, req.Body)
	if err != nil {
		sendErrorResponse(w, "Error BD (draft): "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, models.EmailResponse{
		Success: true,
		Message: fmt.Sprintf("Borrador creado (id=%d)", id),
	})
}

// PUT /drafts/{id}  -> edita borrador solo si status='draft'
func (h *EmailHandler) UpdateDraftHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPut {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/drafts/")
	id64, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id64 <= 0 {
		sendErrorResponse(w, "ID inválido")
		return
	}

	var req DraftRequest
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

	ok, err := h.Store.UpdateDraft(r.Context(), id64, req.To, req.Subject, req.Body)
	if err != nil {
		sendErrorResponse(w, "Error BD (update draft): "+err.Error())
		return
	}
	if !ok {
		sendErrorResponse(w, "No se encontró borrador o ya no está en estado draft")
		return
	}

	writeJSON(w, http.StatusOK, models.EmailResponse{Success: true, Message: "Borrador actualizado"})
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
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, DELETE, PUT")
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

// =========================
//  CRUD adicional (Emails y Templates)
// =========================

// GET /emails?status=&limit=&offset=
func (h *EmailHandler) ListEmailsHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	f := storage.EmailFilter{}
	f.Status = strings.TrimSpace(q.Get("status"))
	if v := q.Get("limit"); v != "" {
		if n, _ := strconv.Atoi(v); n > 0 {
			f.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, _ := strconv.Atoi(v); n >= 0 {
			f.Offset = n
		}
	}

	items, err := h.Store.ListEmails(r.Context(), f)
	if err != nil {
		sendErrorResponse(w, "Error listando emails: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// GET /emails/{id}
func (h *EmailHandler) GetEmailHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/emails/")
	idNum, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || idNum <= 0 {
		sendErrorResponse(w, "ID inválido")
		return
	}

	item, err := h.Store.GetEmailByID(r.Context(), idNum)
	if err != nil {
		sendErrorResponse(w, "Error obteniendo email: "+err.Error())
		return
	}
	if item == nil {
		http.NotFound(w, r)
		return
	}

	writeJSON(w, http.StatusOK, item)
}

// DELETE /emails/{id}
func (h *EmailHandler) DeleteEmailHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/emails/")
	idNum, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || idNum <= 0 {
		sendErrorResponse(w, "ID inválido")
		return
	}

	if err := h.Store.DeleteEmail(r.Context(), idNum); err != nil {
		sendErrorResponse(w, "Error eliminando: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// GET /templates
func (h *EmailHandler) ListTemplatesHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	items, err := h.Store.ListTemplates(r.Context())
	if err != nil {
		sendErrorResponse(w, "Error listando templates: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// GET /templates/{key}/versions
func (h *EmailHandler) ListTemplateVersionsHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	key := strings.TrimPrefix(r.URL.Path, "/templates/")
	key = strings.TrimSuffix(key, "/versions")
	key = strings.TrimSpace(key)
	if key == "" || strings.Contains(key, "/") {
		sendErrorResponse(w, "Template key inválida")
		return
	}
	items, err := h.Store.ListTemplateVersions(r.Context(), key)
	if err != nil {
		sendErrorResponse(w, "Error listando versiones: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type CreateVersionReq struct {
	TemplateKey string  `json:"templateKey"`
	Locale      string  `json:"locale"`
	Subject     string  `json:"subject"`
	BodyHTML    *string `json:"bodyHtml,omitempty"`
	BodyText    *string `json:"bodyText,omitempty"`
	Activate    bool    `json:"activate"`
}

// POST /templates/versions
func (h *EmailHandler) CreateTemplateVersionHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req CreateVersionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendErrorResponse(w, "JSON inválido: "+err.Error())
		return
	}
	if strings.TrimSpace(req.TemplateKey) == "" || strings.TrimSpace(req.Locale) == "" || strings.TrimSpace(req.Subject) == "" {
		sendErrorResponse(w, "templateKey, locale y subject son requeridos")
		return
	}
	htmlNull := sql.NullString{Valid: req.BodyHTML != nil, String: zeroIfNil(req.BodyHTML)}
	textNull := sql.NullString{Valid: req.BodyText != nil, String: zeroIfNil(req.BodyText)}

	if err := h.Store.CreateTemplateVersion(r.Context(), req.TemplateKey, req.Locale, req.Subject, htmlNull, textNull, req.Activate); err != nil {
		sendErrorResponse(w, "Error creando versión: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

type ActivateReq struct {
	TemplateKey string `json:"templateKey"`
	Version     int    `json:"version"`
	Locale      string `json:"locale"`
}

// PUT /templates/activate
func (h *EmailHandler) ActivateTemplateVersionHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPut {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req ActivateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendErrorResponse(w, "JSON inválido: "+err.Error())
		return
	}
	if strings.TrimSpace(req.TemplateKey) == "" || req.Version <= 0 || strings.TrimSpace(req.Locale) == "" {
		sendErrorResponse(w, "templateKey, version y locale son requeridos")
		return
	}
	if err := h.Store.ActivateTemplateVersion(r.Context(), req.TemplateKey, req.Version, req.Locale); err != nil {
		sendErrorResponse(w, "Error activando versión: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func zeroIfNil(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
