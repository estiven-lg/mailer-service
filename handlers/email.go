package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"mailer-service/models"
)

// SendEmailHandler handles POST requests to send emails
func SendEmailHandler(w http.ResponseWriter, r *http.Request) {
	// Set CORS headers
	setCORSHeaders(w)

	// Handle preflight OPTIONS
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Verify method is POST
	if r.Method != "POST" {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	// Decode JSON from request body
	var emailReq models.EmailRequest
	err := json.NewDecoder(r.Body).Decode(&emailReq)
	if err != nil {
		sendErrorResponse(w, "Error decodificando JSON: "+err.Error())
		return
	}

	// Validate required fields
	if emailReq.To == "" || emailReq.Subject == "" || emailReq.Body == "" {
		sendErrorResponse(w, "Todos los campos (to, subject, body) son requeridos")
		return
	}

	// Validate recipient email format
	if !isValidEmail(emailReq.To) {
		sendErrorResponse(w, "Formato de email destinatario inválido")
		return
	}

	// Send email
	err = sendEmail(emailReq)
	if err != nil {
		sendErrorResponse(w, "Error enviando correo: "+err.Error())
		return
	}

	// Send success response
	response := models.EmailResponse{
		Success: true,
		Message: "Correo enviado exitosamente",
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// sendEmail sends the email using SMTP
func sendEmail(email models.EmailRequest) error {
	// SMTP server configuration from environment variables
	smtpHost := getEnv("SMTP_HOST", "smtp.gmail.com")
	smtpPort := getEnv("SMTP_PORT", "587")
	smtpUsername := getEnv("SMTP_USERNAME", "")
	smtpPassword := getEnv("SMTP_PASSWORD", "")
	fromEmail := getEnv("FROM_EMAIL", smtpUsername)
	timeoutStr := getEnv("EMAIL_TIMEOUT", "30")

	// Ensure we have credentials
	if smtpUsername == "" || smtpPassword == "" {
		return fmt.Errorf("credenciales SMTP no configuradas. Verifica SMTP_USERNAME y SMTP_PASSWORD")
	}

	// Convert timeout to integer
	timeout, err := strconv.Atoi(timeoutStr)
	if err != nil {
		timeout = 30
	}

	// Authentication
	auth := smtp.PlainAuth("", smtpUsername, smtpPassword, smtpHost)

	// Build message
	from := fromEmail
	to := []string{email.To}
	
	msg := []byte("To: " + email.To + "\r\n" +
		"From: " + from + "\r\n" +
		"Subject: " + email.Subject + "\r\n" +
		"MIME-version: 1.0;\r\n" +
		"Content-Type: text/html; charset=\"UTF-8\";\r\n" +
		"\r\n" +
		email.Body + "\r\n")

	// Send email with timeout
	err = sendEmailWithTimeout(smtpHost+":"+smtpPort, auth, from, to, msg, time.Duration(timeout)*time.Second)
	if err != nil {
		return fmt.Errorf("error SMTP: %v", err)
	}

	return nil
}

// sendEmailWithTimeout sends email with timeout
func sendEmailWithTimeout(addr string, auth smtp.Auth, from string, to []string, msg []byte, timeout time.Duration) error {
	// This is a basic implementation - consider using an SMTP client with timeout in production
	return smtp.SendMail(addr, auth, from, to, msg)
}

// HealthCheckHandler to check service status
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	
	response := map[string]interface{}{
		"status":    "healthy",
		"service":   "mailer-service",
		"timestamp": time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// setCORSHeaders sets CORS headers
func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

// sendErrorResponse sends an error response
func sendErrorResponse(w http.ResponseWriter, errorMsg string) {
	response := models.EmailResponse{
		Success: false,
		Message: "Error enviando correo",
		Error:   errorMsg,
	}

	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(response)
}

// getEnv fetches environment variables with a default value
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// isValidEmail performs a basic email validation
func isValidEmail(email string) bool {
	// Basic validation - make more robust if needed
	return len(email) > 3 && strings.Contains(email, "@") && strings.Contains(email, ".")
}