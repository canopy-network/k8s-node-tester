package shared

// Accounts represents a collection of main account identities for ids.json
type Accounts struct {
	Accounts map[string]Account `json:"accounts" yaml:"accounts"`
}

// Account represents a main account identity for ids.json
type Account struct {
	Address         string `json:"address" yaml:"address"`
	PublicKey       string `json:"publicKey" yaml:"publicKey"`
	PrivateKey      string `json:"privateKey" yaml:"privateKey"`
	PrivateKeyBytes []byte `json:"-" yaml:"-"` // Not exported to JSON, used for keystore
}
