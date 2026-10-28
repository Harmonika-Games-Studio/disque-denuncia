package web

// dataBR transforma AAAA-MM-DD em DD/MM/AAAA; o que não tiver esse formato volta como veio.
func dataBR(d string) string {
	if len(d) != 10 || d[4] != '-' || d[7] != '-' {
		return d
	}
	return d[8:10] + "/" + d[5:7] + "/" + d[0:4]
}
