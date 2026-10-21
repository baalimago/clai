package sqlitepage

// Kind is the storage class of one column value.
type Kind uint8

const (
	Null Kind = iota
	Integer
	Real
	Text
	Blob
)

// Value is one decoded column value. Only the field named by Kind is set.
type Value struct {
	Kind Kind
	Int  int64
	Real float64
	Text string
	Blob []byte
}
