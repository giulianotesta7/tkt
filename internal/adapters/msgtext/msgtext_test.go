package msgtext

import (
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

func TestRendererResolvesInTheGivenLanguage(t *testing.T) {
	m := application.MsgRef("email.test.body", application.Text("smtp.example.com"))
	es := Renderer{}.Render("es", m)
	if es != "Este es un email de prueba de TKT. Lo entregó tu servidor SMTP smtp.example.com, así que las notificaciones de tickets pueden llegar a tu equipo por email." {
		t.Fatalf("es = %q", es)
	}
	en := Renderer{}.Render("en", application.MsgRef("email.test.subject"))
	if en != "TKT test email" {
		t.Fatalf("en = %q", en)
	}
	nested := application.MsgRef("notifications.kind.state_changed",
		application.Copy("label.state.open"), application.Text("x"))
	if got := (Renderer{}).Render("en", nested); got == "" || got == "notifications.kind.state_changed" {
		t.Fatalf("nested = %q", got)
	}
}
