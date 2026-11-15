package agent

import "testing"

func TestClaimsAndSupport(t *testing.T) {
	result := "| Desk | Price | Weight |\n|---|---|---|\n" +
		"| Alpha Desk | $420 | 31.5 kg [docs](http://docs.test/alpha) |\n" +
		"| Beta Desk | $610 | 38.0 kg [docs](http://docs.test/beta) |\n" +
		"The Gamma Desk folds flat for storage ([source](http://docs.test/gamma)). It is the cheapest.\n"
	claims := Claims(result)
	if len(claims) != 3 || claims[2].Sources[0] != "http://docs.test/gamma" {
		t.Fatalf("claims %+v", claims)
	}
	alpha := "Alpha Desk The Alpha Desk has a bamboo top. Price: $420. Weight: 31.5 kg."
	if !supports(alpha, claims[0].Text) {
		t.Fatalf("the alpha page does not support %q", claims[0].Text)
	}
	if supports(alpha, "| Alpha Desk | $480 | 31.5 kg docs |") {
		t.Fatal("a wrong price passed")
	}
	if !supports("The Gamma Desk folds flat for storage.", claims[2].Text) {
		t.Fatalf("the gamma page does not support %q", claims[2].Text)
	}
	if supports("The Gamma Desk has wheels.", `The desk is "folds flat for storage" here`) {
		t.Fatal("a missing quote passed")
	}
}
