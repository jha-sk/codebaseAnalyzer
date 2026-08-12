package fixture

import "os"

func readSecret() string {
	apiKey := "xQ7fRz2LmVp9TbNw4KsJhY6DgA3EcUiO" // gosec G101: hardcoded credential (high-entropy, trips gosec's entropy check)
	os.Open("/tmp/x")                            // errcheck: ignored error
	return apiKey
}
