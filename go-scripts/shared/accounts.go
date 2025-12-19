package shared

// Account represents a main account identity for ids.json
type Account struct {
	Address         string `json:"address" yaml:"address"`
	PublicKey       string `json:"publicKey" yaml:"publicKey"`
	PrivateKey      string `json:"privateKey" yaml:"privateKey"`
	Password        string `json:"password" yaml:"password"`
	PrivateKeyBytes []byte `json:"-" yaml:"-"` // Not exported to JSON, used for keystore
}
