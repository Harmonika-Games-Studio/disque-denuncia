package limpeza

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// pdfEscrito monta um PDF com xref de verdade. Cada revisão é uma lista de objetos
// (número → corpo); a segunda revisão é salva no fim, como faz o "salvar" de muitos
// programas, e deixa a primeira no arquivo.
func pdfEscrito(revisoes ...map[int]string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	anterior := 0
	for _, objs := range revisoes {
		offsets := map[int]int{}
		for n := 1; n <= 20; n++ {
			if corpo, ok := objs[n]; ok {
				offsets[n] = b.Len()
				fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, corpo)
			}
		}
		xref := b.Len()
		b.WriteString("xref\n0 1\n0000000000 65535 f \n")
		for n := 1; n <= 20; n++ {
			if off, ok := offsets[n]; ok {
				fmt.Fprintf(&b, "%d 1\n%010d 00000 n \n", n, off)
			}
		}
		prev := ""
		if anterior > 0 {
			prev = fmt.Sprintf(" /Prev %d", anterior)
		}
		fmt.Fprintf(&b, "trailer\n<< /Size 10 /Root 1 0 R /Info 4 0 R /ID [<0123456789ABCDEF0123456789ABCDEF> <0123456789ABCDEF0123456789ABCDEF>]%s >>\nstartxref\n%d\n%%%%EOF\n", prev, xref)
		anterior = xref
	}
	return b.Bytes()
}

func stream(dict string, conteudo []byte) string {
	return fmt.Sprintf("<< %s /Length %d >>\nstream\n%s\nendstream", dict, len(conteudo), conteudo)
}

func pdfDoFulano(t *testing.T) []byte {
	_, foto := jpegComMetadados(t)
	foto, _ = JPEG(foto) // tira o lixo depois do fim; o EXIF volta abaixo
	exif := segmento(0xE1, "Exif\x00\x00GPS do Fulano")
	foto = append(append(append([]byte{}, foto[:2]...), exif...), foto[2:]...)
	v1 := map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R /Metadata 5 0 R >>",
		2: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Annots [6 0 R] /PieceInfo << /Illustrator << /Private (C:\\\\Users\\\\fulano) >> >> /Resources << /XObject << /Im1 7 0 R >> >> /Contents 8 0 R >>",
		4: "<< /Author (Fulano de Tal) /Creator (Word) /Producer (Word) /CreationDate (D:20261028143300-03'00') >>",
		5: stream("/Type /Metadata /Subtype /XML", []byte("<x:xmpmeta><dc:creator>Fulano</dc:creator></x:xmpmeta>")),
		6: "<< /Type /Annot /Subtype /Text /Rect [0 0 10 10] /T (Fulano) /M (D:20261028143300) /CreationDate (D:20261028143300) /Contents (comentario) >>",
		7: stream("/Type /XObject /Subtype /Image /Width 16 /Height 16 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode", foto),
		8: stream("", []byte("q 16 0 0 16 0 0 cm /Im1 Do Q")),
	}
	v2 := map[int]string{4: "<< /Author (Anonimo) >>"}
	return pdfEscrito(v1, v2)
}

// conteudoDoPDF lê o PDF com o pdfcpu e junta o texto de todos os objetos e streams
// (os objetos regravados ficam comprimidos: procurar nos bytes crus não basta).
func conteudoDoPDF(t *testing.T, b []byte) (string, *model.Context) {
	t.Helper()
	conf := model.NewStatelessConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	ctx, err := api.ReadValidateAndOptimize(context.Background(), bytes.NewReader(b), conf, nil)
	if err != nil {
		t.Fatalf("o PDF limpo não abre: %v", err)
	}
	var tudo strings.Builder
	for _, e := range ctx.Table {
		if e == nil || e.Object == nil {
			continue
		}
		tudo.WriteString(e.Object.String())
		if sd, ok := e.Object.(types.StreamDict); ok {
			if sd.Decode() == nil {
				tudo.Write(sd.Content)
			}
			tudo.Write(sd.Raw)
		}
	}
	return tudo.String(), ctx
}

func TestPDFTiraAutorXMPAnotacaoEVersaoAntiga(t *testing.T) {
	original := pdfDoFulano(t)
	if antes, _ := conteudoDoPDF(t, original); !strings.Contains(antes, "Fulano") {
		t.Fatal("o PDF de teste não tem o que limpar")
	}
	if Identificar(original) != FormatoPDF {
		t.Fatal("não identificou")
	}
	limpo, err := PDF(original)
	if err != nil {
		t.Fatal(err)
	}
	tudo, ctx := conteudoDoPDF(t, limpo)
	for _, s := range []string{"Fulano", "fulano", "Word", "2026", "pdfcpu", "Anonimo", "PieceInfo", "xmpmeta"} {
		if strings.Contains(tudo, s) || bytes.Contains(limpo, []byte(s)) {
			t.Errorf("sobrou %q", s)
		}
	}
	if !strings.Contains(tudo, "comentario") || !strings.Contains(tudo, "/Im1 Do") {
		t.Error("tirou o conteúdo junto")
	}
	if ctx.Info == nil {
		t.Fatal("sem Info")
	}
	if info, _ := ctx.DereferenceDict(*ctx.Info); len(info) != 0 {
		t.Errorf("Info não ficou vazio: %v", info)
	}
	if _, ok := ctx.RootDict.Find("Metadata"); ok {
		t.Error("o catálogo ainda aponta para o XMP")
	}
}

// O ID que o pdfcpu gera é o md5 da hora atual; o gravado tem que ser aleatório.
func TestPDFIDAleatorio(t *testing.T) {
	a, err := PDF(pdfDoFulano(t))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := PDF(pdfDoFulano(t))
	idA, idB := reID.Find(a), reID.Find(b)
	if idA == nil || bytes.Equal(idA, idB) || bytes.Contains(idA, []byte("0123456789ABCDEF")) {
		t.Errorf("IDs: %s / %s", idA, idB)
	}
	m := reID.FindSubmatch(a)
	if len(m[1]) != 32 || len(m[2]) != 32 || bytes.Equal(m[1], m[2]) {
		t.Errorf("ID: %s", idA)
	}
}

func TestPDFRecusa(t *testing.T) {
	if _, err := PDF([]byte("%PDF-1.4\nlixo")); !errors.Is(err, ErrFormato) {
		t.Errorf("lixo: %v", err)
	}
	if _, err := trocarID([]byte("%PDF sem trailer")); !errors.Is(err, ErrFormato) {
		t.Errorf("sem ID: %v", err)
	}
}

func TestPDFFotoQueNaoEJPEGFicaComoEsta(t *testing.T) {
	sd := types.NewStreamDict(types.NewDict(), 0, nil, nil, []types.PDFFilter{{Name: "DCTDecode"}})
	sd.Raw = []byte("não é jpeg")
	if got := limparFoto(sd); !bytes.Equal(got.Raw, sd.Raw) {
		t.Error("mexeu no stream ilegível")
	}
	sd.FilterPipeline = []types.PDFFilter{{Name: "FlateDecode"}, {Name: "DCTDecode"}}
	if got := limparFoto(sd); !bytes.Equal(got.Raw, sd.Raw) {
		t.Error("mexeu em stream com mais de um filtro")
	}
}
