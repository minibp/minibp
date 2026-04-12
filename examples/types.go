// types.go - Go types
package api

type Config struct {
	Host string
	Port int
}

type Response struct {
	Data  string
	Error error
}
