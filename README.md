# Microservicio de Envío de Emails

Un microservicio simple en Go para enviar correos electrónicos a través de SMTP.

---

## Configuración Rápida

### 1. Variables de Entorno

Crea un archivo `.env` en la raíz del proyecto con la siguiente configuración:

```env
# Configuración SMTP (Gmail ejemplo)
SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_USERNAME=tu_email@gmail.com
SMTP_PASSWORD=tu-contraseña-de-aplicación

# Configuración del servidor
SERVER_PORT=8080
SERVER_HOST=localhost

# Timeout en segundos para envío de emails
EMAIL_TIMEOUT=30
```

### 2. Configuración para Gmail

Si usas Gmail, necesitas:

1. Activar la verificación en 2 pasos en tu cuenta de Google.
2. Generar una contraseña de aplicación:
   - Ve a: `Google Account → Security → 2-Step Verification → App passwords`
   - Genera una contraseña para "Mail".
   - Usa esa contraseña en `SMTP_PASSWORD`.

### 3. Ejecutar el Servicio

```bash
# Instalar dependencias
go mod tidy

# Ejecutar
go run main.go
```

El servicio estará disponible en: `http://localhost:8080`

---

## Uso

### Endpoints

- `POST /send-email` - Enviar correo electrónico  
- `GET /health` - Verificar estado del servicio  

### Ejemplo de envío de correo

```bash
curl -X POST http://localhost:8080/send-email \
  -H "Content-Type: application/json" \
  -d '{
    "to": "destinatario@example.com",
    "subject": "Asunto del correo",
    "body": "<h1>Hola</h1><p>Este es un correo de prueba</p>"
  }'
