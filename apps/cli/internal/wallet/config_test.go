package wallet

import "testing"

func TestPaymentSignersRequireMatchingConfiguration(t *testing.T) {
	for _, test := range []struct {
		name    string
		rpc     string
		network string
		program string
		valid   bool
	}{
		{"localnet", "http://127.0.0.1:8899", "localnet", ProgramID, true},
		{"localhost", "http://localhost:8899/", "localnet", ProgramID, true},
		{"devnet", "https://api.devnet.solana.com/", "devnet", ProgramID, true},
		{"devnet plan on localnet", "http://127.0.0.1:8899", "devnet", ProgramID, false},
		{"localnet plan on devnet", "https://api.devnet.solana.com", "localnet", ProgramID, false},
		{"missing RPC", "", "localnet", ProgramID, false},
		{"mainnet RPC", "https://api.mainnet-beta.solana.com", "devnet", ProgramID, false},
		{"unknown RPC", "https://example.test", "devnet", ProgramID, false},
		{"missing program", "http://127.0.0.1:8899", "localnet", "", false},
		{"different program", "http://127.0.0.1:8899", "localnet", testWallet(6).Address(), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			poster, post, funding := fundingFixture(t, 3)
			reviewer, reviewPost, settlement := settlementFixture(t, "release", "independent")
			funding.Network, settlement.Escrow.Network = test.network, test.network

			t.Setenv("SOLANA_RPC_URL", test.rpc)
			t.Setenv("SOLANA_PROGRAM_ID", test.program)

			_, fundingErr := poster.SignFunding(post, funding)

			_, settlementErr := reviewer.SignSettlement(reviewPost, settlement)
			if (fundingErr == nil) != test.valid || (settlementErr == nil) != test.valid {
				t.Fatalf("funding=%v settlement=%v, valid=%v", fundingErr, settlementErr, test.valid)
			}
		})
	}
}
