-- Login do painel do admin (HTTP Basic). password_hash é bcrypt; gere com:
--   htpasswd -nBC 10 usuario | cut -d: -f2
-- e insira com: sqlite3 /data/denuncias.db "INSERT INTO users VALUES ('usuario', 'hash')"
CREATE TABLE users (
	username      TEXT PRIMARY KEY,
	password_hash TEXT NOT NULL
);
