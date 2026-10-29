// Package limpeza tira os metadados dos anexos que não são foto: vídeo e áudio do
// celular (MP4, MOV, M4A, 3GP), MP3, GIF, documentos do Office e do LibreOffice, PDF e
// ZIP comum (reaberto, com cada arquivo de dentro limpo pelo tipo dele). As fotos
// (JPG, PNG, WebP) são refeitas do zero no pacote imagem.
//
// Dois jeitos, conforme o formato:
//
//   - Remendos, no mesmo lugar (vídeo, áudio): o arquivo não muda de tamanho nem de
//     estrutura. O bloco de metadados vira um bloco vazio do mesmo tamanho e as datas
//     são zeradas. Funciona num vídeo de 5 GB sem lê-lo inteiro: só os cabeçalhos são lidos.
//   - Arquivo novo (GIF, Office, PDF, ZIP): o arquivo é lido inteiro e regravado sem os
//     campos de autor, datas, caminhos e programa. Por isso esses têm teto de tamanho
//     (o servidor guarda tudo na memória).
//
// O que nenhum dos dois resolve, e a página avisa: a voz num áudio, o que aparece num
// vídeo, um nome escrito no documento, uma assinatura escaneada.
package limpeza

import (
	"bytes"
	"errors"
)

// ErrFormato: os bytes não são do formato esperado (ou estão corrompidos). Quem
// chama deixa o arquivo como veio e o marca como não limpo.
var ErrFormato = errors.New("formato não reconhecido")

// Remendo são bytes a gravar por cima, a partir de Pos. Nunca muda o tamanho do arquivo.
type Remendo struct {
	Pos   int64
	Dados []byte
}

// Formato é o que a limpeza sabe tratar, pelo começo do arquivo.
type Formato int

const (
	Nenhum     Formato = iota
	FormatoMP4         // caixas ISO BMFF / QuickTime: MP4, MOV, M4A, 3GP
	FormatoMP3
	FormatoGIF
	FormatoZip // Office/LibreOffice (Office confirma) ou ZIP comum (ZIP)
	FormatoPDF
)

// Identificar olha os primeiros bytes (pelo menos 12). MP3 sem etiqueta ID3 no começo
// não é reconhecido: é raro, e o começo de um MP3 sem etiqueta se confunde com dado
// aleatório.
func Identificar(cabeca []byte) Formato {
	switch {
	case len(cabeca) >= 12 && caixaDeTopo(string(cabeca[4:8])):
		return FormatoMP4
	case bytes.HasPrefix(cabeca, []byte("ID3")):
		return FormatoMP3
	case bytes.HasPrefix(cabeca, []byte("GIF87a")), bytes.HasPrefix(cabeca, []byte("GIF89a")):
		return FormatoGIF
	case bytes.HasPrefix(cabeca, []byte("PK\x03\x04")):
		return FormatoZip
	case bytes.HasPrefix(cabeca, []byte("%PDF-")):
		return FormatoPDF
	}
	return Nenhum
}
