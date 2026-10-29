package limpeza

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// PDF. O que identifica quem fez:
//
//   - o dicionário Info (Author, Creator, Producer, datas) e o XMP (a mesma coisa em
//     XML, às vezes com o caminho do arquivo e o login), no documento e em cada página
//     ou imagem;
//   - versões anteriores do documento, que o "salvar" de muitos programas deixa no fim
//     do arquivo (dá para recuperar o texto apagado e o autor antigo);
//   - autor e datas de cada anotação ou comentário (/T, /M, /CreationDate);
//   - PieceInfo (dados privados do Illustrator e do Acrobat, com caminhos);
//   - o EXIF das fotos escaneadas dentro dele.
//
// O pdfcpu lê o PDF, os campos acima são tirados, e ele é regravado do zero: só o que a
// versão atual usa é escrito, então as versões antigas ficam para trás.
//
// Dois cuidados com o pdfcpu, porque o que ele carimba ao gravar contaria a HORA do
// envio, que este canal não guarda: (1) sem Info, ele cria um com CreationDate e ModDate
// de agora; por isso vai um Info vazio e PreserveInfoDict; (2) o ID do arquivo que ele
// gera é o md5 da hora atual; por isso o ID gravado é trocado por bytes aleatórios
// (trocarID). Configuração sem estado (não lê nem cria config.yml no disco) e sem rede
// (o pdfcpu sabe checar links e revogação de certificado pela internet).

// chavesDeMetadado saem de qualquer dicionário do PDF. Em anotações (comentários),
// T é o nome de quem comentou, e M e CreationDate, as datas.
var chavesDeMetadado = []string{"Metadata", "PieceInfo", "LastModified"}

var chavesDeAnotacao = []string{"T", "M", "CreationDate"}

// PDF regrava o PDF sem metadados. ErrFormato se o pdfcpu não consegue ler (corrompido,
// ou protegido por senha de abertura).
func PDF(dados []byte) ([]byte, error) {
	conf := model.NewStatelessConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	conf.PreserveInfoDict = true
	conf.Offline = true // nada de sair na internet (checar links, revogação de assinatura)
	conf.ValidateLinks = false
	ctx, err := api.ReadValidateAndOptimize(context.Background(), bytes.NewReader(dados), conf, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormato, err)
	}
	for _, e := range ctx.Table {
		if e != nil && !e.Free && e.Object != nil {
			e.Object = limparObjeto(e.Object)
		}
	}
	for _, k := range chavesDeMetadado {
		ctx.RootDict.Delete(k)
	}
	info, err := ctx.IndRefForNewObject(types.NewDict())
	if err != nil {
		return nil, err
	}
	ctx.Info = info

	var out bytes.Buffer
	if err := api.WriteContext(context.Background(), ctx, &out); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormato, err)
	}
	return trocarID(out.Bytes())
}

func limparObjeto(o types.Object) types.Object {
	switch v := o.(type) {
	case types.Dict:
		limparDict(v)
	case types.StreamDict:
		limparDict(v.Dict)
		return limparFoto(v)
	}
	return o
}

func limparDict(d types.Dict) {
	for _, k := range chavesDeMetadado {
		d.Delete(k)
	}
	if t := d.Type(); t != nil && *t == "Annot" {
		for _, k := range chavesDeAnotacao {
			d.Delete(k)
		}
	}
}

// limparFoto tira o EXIF de uma imagem JPEG do PDF (filtro DCTDecode sozinho: o conteúdo
// do stream é o arquivo JPEG). Se o JPEG não for legível, fica como está.
func limparFoto(sd types.StreamDict) types.StreamDict {
	if len(sd.FilterPipeline) != 1 || sd.FilterPipeline[0].Name != "DCTDecode" || sd.Raw == nil {
		return sd
	}
	limpo, err := JPEG(sd.Raw)
	if err != nil {
		return sd
	}
	n := int64(len(limpo))
	sd.Raw = limpo
	sd.StreamLength = &n
	sd.Update("Length", types.Integer(n))
	return sd
}

var reID = regexp.MustCompile(`/ID\s*\[\s*<([0-9A-Fa-f]*)>\s*<([0-9A-Fa-f]*)>\s*\]`)

// trocarID troca o ID do arquivo (no trailer ou no dicionário do xref) por bytes
// aleatórios do mesmo tamanho. Como o tamanho não muda, nenhum deslocamento do xref
// precisa ser refeito.
func trocarID(b []byte) ([]byte, error) {
	achou := false
	b = reID.ReplaceAllFunc(b, func(m []byte) []byte {
		achou = true
		partes := reID.FindSubmatch(m)
		out := string(m)
		for _, p := range partes[1:] {
			out = strings.Replace(out, "<"+string(p)+">", "<"+aleatorio(len(p))+">", 1)
		}
		return []byte(out)
	})
	if !achou {
		return nil, fmt.Errorf("%w: PDF regravado sem ID", ErrFormato)
	}
	return b, nil
}

func aleatorio(digitos int) string {
	b := make([]byte, (digitos+1)/2)
	rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))[:digitos]
}
