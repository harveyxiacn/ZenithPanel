package terminal

import (
	"testing"
	"time"
)

func TestTicketIsIPBoundAndSingleUse(t *testing.T) {
	ticket, expires, err := IssueTicket("admin:test", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if ticket == "" || time.Until(expires) <= 0 || time.Until(expires) > ticketTTL {
		t.Fatalf("invalid ticket expiry: %q %v", ticket, expires)
	}
	principal, ok := ConsumeTicket(ticket, "192.0.2.10")
	if !ok || principal != "admin:test" {
		t.Fatalf("consume=%q,%v", principal, ok)
	}
	if _, ok := ConsumeTicket(ticket, "192.0.2.10"); ok {
		t.Fatal("ticket was reusable")
	}
}

func TestTicketWrongIPIsRejectedAndConsumed(t *testing.T) {
	ticket, _, err := IssueTicket("admin:test", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ConsumeTicket(ticket, "192.0.2.11"); ok {
		t.Fatal("ticket accepted from wrong IP")
	}
	if _, ok := ConsumeTicket(ticket, "192.0.2.10"); ok {
		t.Fatal("wrong-IP attempt did not consume ticket")
	}
}
