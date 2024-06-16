package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

var db *sql.DB

type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

func main() {

	var dbPort string = "1234"

	fmt.Println("PORT is set to", dbPort)
	connStr := "postgres://nexaUser:nexaPass@localhost:5432/nexasecure-db?sslmode=disable"

	var errDB error
	db, errDB = sql.Open("postgres", connStr)
	if errDB != nil {
		log.Fatal("Database connection error:", errDB)
		return
	}

	dbConnErr := db.Ping()
	if dbConnErr != nil {
		log.Fatal("Database connection failed \n", dbConnErr)
		return
	}

	fmt.Println("Router is initiating to", dbPort)
	MainRouter := chi.NewRouter()
	MainRouter.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://"},
		AllowedMethods:   []string{"GET", "POST"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	fmt.Println("Initiating the Auth Router at ", dbPort)
	AuthRoutes := chi.NewRouter()
	AuthRoutes.Get("/healthz", HandlerReadiness)
	AuthRoutes.Get("/error", HandlerError)
	AuthRoutes.Post("/login", HandlerLogin)
	AuthRoutes.Post("/logout", HandlerLogout)
	MainRouter.Mount("/auth", AuthRoutes)

	Server := &http.Server{
		Addr:    ":" + dbPort,
		Handler: MainRouter,
	}

	fmt.Println("Server is running on port", dbPort)
	serverErr := Server.ListenAndServe()
	if serverErr != nil {
		log.Fatal(serverErr)
	}
}

func responseWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	response, err := json.Marshal(payload)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}

func responseWithError(w http.ResponseWriter, code int, message string) {
	if code > 499 {
		log.Fatal(message)
	}

	type errResponse struct {
		Error string `json:"error"`
	}

	responseWithJSON(w, code, errResponse{Error: message})
}

func HandlerReadiness(w http.ResponseWriter, r *http.Request) {
	responseWithJSON(w, 200, map[string]string{"status": "ok"})
}

func HandlerError(w http.ResponseWriter, r *http.Request) {
	responseWithError(w, 400, "Something went wrong")
}

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type User struct {
	uniqueid uuid.UUID `json:"uniqueid"`
	Username string    `json:"username"`
	Password string    `json:"password"`
	// Add more fields as needed
}

func HandlerLogin(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var creds Credentials
	err := json.NewDecoder(r.Body).Decode(&creds)
	if err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	user, err := getUserByUsername(creds.Username)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Invalid credentials", http.StatusUnauthorized)

		} else {
			http.Error(w, "Database error", http.StatusInternalServerError)
			fmt.Println("Error querying database for user:", err)
		}
		return
	}

	// Compare the stored password hash with the password sent in the request
	if user.Password != creds.Password {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		responseWithJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid credentials"})
		return
	}

	// If credentials are valid, return success response (you can set cookies, JWT tokens, etc.)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Login successful"))
}

func extractTokenFromRequest(r *http.Request) string {
	// Example: Extract token from Authorization header for JWT
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		// Assuming format: "Bearer <token>"
		tokenParts := strings.Split(authHeader, " ")
		if len(tokenParts) == 2 && tokenParts[0] == "Bearer" {
			return tokenParts[1]
		}
	}

	cookie, err := r.Cookie("auth_token")
	if err == nil {
		return cookie.Value
	}

	return "Token not found"
}

func LogEvent(eventType string, details interface{}) error {
	// Example: Log event to a file
	logFile, err := os.OpenFile("app.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	logEntry := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), eventType)
	if details != nil {
		// Marshal details to JSON
		detailsJSON, err := json.Marshal(details)
		if err != nil {
			return err
		}
		logEntry += string(detailsJSON) + "\n"
	}

	if _, err := logFile.WriteString(logEntry); err != nil {
		return err
	}

	return nil
}

func getUserIdFromToken(tokenString string) (string, error) {
	// Example: Parse JWT token to extract user ID
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Check token signing method here if applicable
		return []byte("your-secret-key"), nil
	})
	if err != nil {
		return "", err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		// Extract user ID from JWT claims
		userID := claims["user_id"].(string)
		return userID, nil
	}

	return "", errors.New("Invalid token or claims")
}

var (
	blacklistMutex sync.Mutex
	blacklist      = make(map[string]struct{})
)

// InvalidateToken invalidates the given token.
func InvalidateToken(token string) error {
	if IsTokenInvalid(token) {
		return errors.New("Token already invalidated")
	}
	// Add token to blacklist
	blacklistMutex.Lock()
	defer blacklistMutex.Unlock()
	blacklist[token] = struct{}{}

	return nil
}

// IsTokenInvalid checks if the given token is invalidated.
func IsTokenInvalid(token string) bool {
	blacklistMutex.Lock()
	defer blacklistMutex.Unlock()
	_, invalid := blacklist[token]
	return invalid
}

func HandlerLogout(w http.ResponseWriter, r *http.Request) {
	token := extractTokenFromRequest(r) // Implement function to extract token from request

	if token != "" {
		// Invalidate the token
		err := InvalidateToken(token)
		if err != nil {
			// Handle error
			responseWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to invalidate token"})
			return
		}

		// Clear the authentication cookie
		cookie := http.Cookie{
			Name:     "auth_token",
			Value:    "",
			Path:     "/",
			Expires:  time.Unix(0, 0),
			HttpOnly: true,
		}
		http.SetCookie(w, &cookie)

		// Get the user ID from the token
		userID, err := getUserIdFromToken(token)
		if err != nil {
			// Handle error
			responseWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to get user ID from token"})
			return
		}

		// Log the logout event
		LogEvent("logout", map[string]interface{}{"user_id": userID})

		// Send a success response to the client
		responseWithJSON(w, http.StatusOK, map[string]string{"message": "Logout successful"})
	} else {
		responseWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Missing token"})
	}
}

func getUserByUsername(usern string) (User, error) {
	var user User
	err := db.QueryRow("SELECT username, password FROM users WHERE username = $1", usern).Scan(&user.Username, &user.Password)
	if err != nil {
		if err == sql.ErrNoRows {
			return User{}, fmt.Errorf("no user found with username: %s", usern)
		}
		return User{}, err
	}
	return user, nil
}
