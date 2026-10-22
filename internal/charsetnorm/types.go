package charsetnorm

type langFreq struct {
	lang  string
	chars []rune
}

type uniRange struct {
	name        string
	start, stop rune
}
