package db

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

func open(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, path
}

func TestOpenAplicaMigrationsUmaVez(t *testing.T) {
	conn, path := open(t)
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("nenhuma migration registrada")
	}
	_, err := conn.Exec(`INSERT INTO denuncias (id, categoria, descricao, recebida_em) VALUES ('d1', 'outro', 'x', '2026-10-28')`)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	// Reabrir não pode rodar de novo os CREATE TABLE (dariam erro) nem perder dados.
	conn, err = Open(path)
	if err != nil {
		t.Fatalf("reabrindo: %v", err)
	}
	defer conn.Close()
	var again, denuncias int
	conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&again)
	conn.QueryRow(`SELECT COUNT(*) FROM denuncias`).Scan(&denuncias)
	if again != n || denuncias != 1 {
		t.Errorf("migrations %d→%d, denúncias %d", n, again, denuncias)
	}
}

// A lista de colunas é o contrato do anonimato: coluna nova aqui é decisão
// consciente (e revisão do README), nunca um acréscimo distraído.
func TestColunasNaoIdentificamQuemEnviou(t *testing.T) {
	conn, _ := open(t)
	for tabela, esperadas := range map[string][]string{
		"denuncias": {"id", "categoria", "descricao", "recebida_em", "status", "parecer", "atualizada_em"},
		"anexos":    {"id", "denuncia_id", "posicao", "jpeg"},
	} {
		if got := colunas(t, conn, tabela); !slices.Equal(got, esperadas) {
			t.Errorf("%s: colunas %v, esperava %v", tabela, got, esperadas)
		}
	}
}

// Sem rowid, o SQLite não guarda a ordem em que as linhas chegaram.
func TestTabelasSemRowid(t *testing.T) {
	conn, _ := open(t)
	for _, tabela := range []string{"denuncias", "anexos"} {
		if _, err := conn.Exec(`SELECT rowid FROM ` + tabela); err == nil {
			t.Errorf("%s tem rowid", tabela)
		}
	}
}

func TestPragmas(t *testing.T) {
	conn, _ := open(t)
	for _, p := range []string{"foreign_keys", "secure_delete"} {
		var v int
		if err := conn.QueryRow(`PRAGMA ` + p).Scan(&v); err != nil || v != 1 {
			t.Errorf("%s = %d (%v), esperava 1", p, v, err)
		}
	}
}

func TestApagarDenunciaLevaAnexos(t *testing.T) {
	conn, _ := open(t)
	mustExec(t, conn, `INSERT INTO denuncias (id, categoria, descricao, recebida_em) VALUES ('d1', 'outro', 'x', '2026-10-28')`)
	mustExec(t, conn, `INSERT INTO anexos (id, denuncia_id, posicao, jpeg) VALUES ('a1', 'd1', 0, x'ff')`)
	mustExec(t, conn, `DELETE FROM denuncias WHERE id = 'd1'`)
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM anexos`).Scan(&n)
	if n != 0 {
		t.Errorf("sobraram %d anexos", n)
	}
}

func TestOpenFalhaEmCaminhoInvalido(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "nao", "existe", "t.db")); err == nil {
		t.Error("esperava erro")
	}
}

func colunas(t *testing.T, conn *sql.DB, tabela string) []string {
	t.Helper()
	rows, err := conn.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, tabela)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		out = append(out, n)
	}
	return out
}

func mustExec(t *testing.T, conn *sql.DB, q string) {
	t.Helper()
	if _, err := conn.Exec(q); err != nil {
		t.Fatal(err)
	}
}
