package main

import "testing"

// A trimmed but realistic AmazonEC2 on-demand PriceList document (as returned in GetProducts'
// PriceList slice, one JSON string per product).
const instanceDoc = `{
  "product": {"attributes": {"instanceType": "t4g.medium"}},
  "terms": {
    "OnDemand": {
      "ABC123.JRTCKXETXF": {
        "priceDimensions": {
          "ABC123.JRTCKXETXF.6YS6EN2CT7": {
            "unit": "Hrs",
            "pricePerUnit": {"USD": "0.0336000000"}
          }
        }
      }
    }
  }
}`

const ebsDoc = `{
  "product": {"attributes": {"volumeApiName": "gp3"}},
  "terms": {
    "OnDemand": {
      "XYZ.JRTCKXETXF": {
        "priceDimensions": {
          "XYZ.JRTCKXETXF.6YS6EN2CT7": {
            "unit": "GB-Mo",
            "pricePerUnit": {"USD": "0.0880000000"}
          }
        }
      }
    }
  }
}`

func TestParseOnDemandUSD(t *testing.T) {
	got, err := parseOnDemandUSD(instanceDoc)
	if err != nil {
		t.Fatalf("parse instance doc: %v", err)
	}
	if got != 0.0336 {
		t.Fatalf("instance USD = %v, want 0.0336", got)
	}

	got, err = parseOnDemandUSD(ebsDoc)
	if err != nil {
		t.Fatalf("parse ebs doc: %v", err)
	}
	if got != 0.088 {
		t.Fatalf("ebs USD = %v, want 0.088", got)
	}
}

func TestParseOnDemandUSDErrors(t *testing.T) {
	if _, err := parseOnDemandUSD("{not json"); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
	if _, err := parseOnDemandUSD(`{"terms":{"OnDemand":{}}}`); err == nil {
		t.Fatal("expected an error when no on-demand price is present")
	}
	bad := `{"terms":{"OnDemand":{"o":{"priceDimensions":{"d":{"pricePerUnit":{"USD":"not-a-number"}}}}}}}`
	if _, err := parseOnDemandUSD(bad); err == nil {
		t.Fatal("expected an error for an unparseable USD value")
	}
}

func TestPriceBook(t *testing.T) {
	b := NewPriceBook(0.005)
	if b.HasInstance("t4g.medium") {
		t.Fatal("new book should not have any instance rate")
	}
	b.SetInstanceHourly("t4g.medium", 0.0336)
	b.SetEBSGBMonth("gp3", 0.088)

	if v, ok := b.InstanceHourly("t4g.medium"); !ok || v != 0.0336 {
		t.Fatalf("InstanceHourly = %v, %v", v, ok)
	}
	if v, ok := b.EBSGBMonth("gp3"); !ok || v != 0.088 {
		t.Fatalf("EBSGBMonth = %v, %v", v, ok)
	}
	if !b.HasInstance("t4g.medium") || !b.HasEBS("gp3") {
		t.Fatal("Has* should report cached rates")
	}
	if b.PublicIPv4Hourly() != 0.005 {
		t.Fatalf("PublicIPv4Hourly = %v, want 0.005", b.PublicIPv4Hourly())
	}
}
