package main

const FileExtension = ".md"

type Git struct {
	url  string
	push bool
}

type Csrf struct {
	keyfile  string
	insecure bool
	key      []byte
}

type Listen struct {
	address string
	network string
}

// Config ...
type Config struct {
	listen   Listen
	data     string
	brand    string
	bind     string
	git      Git
	csrf     Csrf
	indexdir string
}
