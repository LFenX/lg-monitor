package secrets

type PathPolicy interface {
	Check(string) error
	ProtectTree(string, bool) error
}
