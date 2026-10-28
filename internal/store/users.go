package store

import (
	"database/sql"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// Authenticate confere usuário e senha contra o hash bcrypt da tabela users.
func (s *Store) Authenticate(username, password string) (bool, error) {
	var hash string
	err := s.db.QueryRow(`SELECT password_hash FROM users WHERE username = ?`, username).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil, nil
}
