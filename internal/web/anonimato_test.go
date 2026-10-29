package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Este teste é a trava do anonimato no código: ele lê todo o fonte do projeto
// (Go e templ, menos testes e código gerado) e reprova o que identificaria quem
// denunciou. Para mudar uma destas regras, mude o README junto: é o que a
// empresa promete a quem denuncia.

var proibidos = map[string]string{
	"RemoteAddr":       "IP de quem acessa",
	"X-Forwarded-For":  "IP repassado pelo proxy",
	"X-Real-Ip":        "IP repassado pelo proxy",
	"Cf-Connecting-Ip": "IP repassado pela Cloudflare",
	"User-Agent":       "navegador/aparelho de quem acessa",
	"UserAgent()":      "navegador/aparelho de quem acessa",
	"Referer()":        "página de onde a pessoa veio",
	".Cookie(":         "cookie (rastreia quem volta)",
	".Cookies()":       "cookie (rastreia quem volta)",
	"SetCookie":        "cookie (rastreia quem volta)",
	"http.Cookie":      "cookie (rastreia quem volta)",
	"unpkg.com":        "recurso de terceiro (entrega o IP a outro servidor)",
	"jsdelivr":         "recurso de terceiro (entrega o IP a outro servidor)",
	"cdnjs":            "recurso de terceiro (entrega o IP a outro servidor)",
	"googleapis":       "recurso de terceiro (entrega o IP a outro servidor)",
	"analytics":        "rastreamento",
	".Filename":        "nome do arquivo enviado (pode ter o nome da pessoa)",
	// No navegador (canal.js): nada guardado, nada lido do aparelho.
	"document.cookie": "cookie (rastreia quem volta)",
	"localStorage":    "guarda dado no navegador da pessoa",
	"sessionStorage":  "guarda dado no navegador da pessoa",
	"indexedDB":       "guarda dado no navegador da pessoa",
	"navigator.":      "lê dado do aparelho (modelo, idioma, localização)",
	"eval(":           "executa código montado em tempo de execução",
	"new Function":    "executa código montado em tempo de execução",
}

// O único cabeçalho que o código lê é o HX-Request (página inteira ou pedaço).
var leituraDeCabecalho = regexp.MustCompile(`Header\.(Get|Values)\(\s*("[^"]*"|[^)]*)\)`)

func fontes(t *testing.T) map[string]string {
	t.Helper()
	raiz, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	err = filepath.WalkDir(raiz, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		nome := d.Name()
		fonte := strings.HasSuffix(nome, ".go") || strings.HasSuffix(nome, ".templ") || strings.HasSuffix(nome, ".js")
		// htmx.min.js é a biblioteca de terceiros, conferida pelo hash no README.
		if d.IsDir() || !fonte || nome == "htmx.min.js" || strings.HasSuffix(nome, "_test.go") || strings.HasSuffix(nome, "_templ.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		out[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFonteNaoIdentificaQuemDenuncia(t *testing.T) {
	arquivos := fontes(t)
	if _, ok := arquivos[must(filepath.Abs("static/canal.js"))]; !ok {
		t.Fatal("o canal.js não está sendo conferido")
	}
	if len(arquivos) < 10 {
		t.Fatalf("achou só %d arquivos de fonte", len(arquivos))
	}
	for p, src := range arquivos {
		for termo, porque := range proibidos {
			if strings.Contains(src, termo) {
				t.Errorf("%s usa %q: %s", p, termo, porque)
			}
		}
		for _, m := range leituraDeCabecalho.FindAllStringSubmatch(src, -1) {
			if m[2] != `"HX-Request"` {
				t.Errorf("%s lê o cabeçalho %s: só HX-Request é permitido", p, m[2])
			}
		}
	}
}

func must(s string, err error) string {
	if err != nil {
		panic(err)
	}
	return s
}

// O próprio teste precisa enxergar uma violação, senão ele passa por não olhar nada.
func TestRegrasPegamViolacao(t *testing.T) {
	src := `ip := r.RemoteAddr; ua := r.Header.Get("User-Agent")`
	if !strings.Contains(src, "RemoteAddr") {
		t.Fatal("regra de IP não pega")
	}
	m := leituraDeCabecalho.FindAllStringSubmatch(src, -1)
	if len(m) != 1 || m[0][2] != `"User-Agent"` {
		t.Fatalf("regex de cabeçalho: %v", m)
	}
}
