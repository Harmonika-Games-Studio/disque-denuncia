package store

type Opcao struct {
	Valor  string
	Rotulo string
}

// Categorias na ordem em que aparecem no formulário.
var Categorias = []Opcao{
	{"assedio_moral", "Assédio moral"},
	{"assedio_sexual", "Assédio sexual"},
	{"discriminacao", "Discriminação ou preconceito"},
	{"fraude", "Fraude, desvio ou corrupção"},
	{"conflito_interesses", "Conflito de interesses"},
	{"seguranca", "Segurança ou saúde no trabalho"},
	{"conduta", "Conduta inadequada"},
	{"outro", "Outro assunto"},
}

var Status = []Opcao{
	{"nova", "Nova"},
	{"em_analise", "Em análise"},
	{"concluida", "Concluída"},
}

// Rotulo devolve o rótulo do valor, ou o próprio valor se ele não estiver na lista.
func Rotulo(lista []Opcao, valor string) string {
	for _, o := range lista {
		if o.Valor == valor {
			return o.Rotulo
		}
	}
	return valor
}

func valido(lista []Opcao, valor string) bool {
	for _, o := range lista {
		if o.Valor == valor {
			return true
		}
	}
	return false
}
