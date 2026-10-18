package skill

import (
	"os"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
)

func TestMain(m *testing.M) {
	i18n.SetDefault(i18n.ZhCN)
	os.Exit(m.Run())
}
