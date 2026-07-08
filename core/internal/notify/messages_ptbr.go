package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/report"
	"github.com/mateusgms/cardpit/core/internal/store"
)

// All user-facing Telegram strings live here, in pt-BR.

func msgStart(in StartInfo) string {
	ev := in.Ev
	var b strings.Builder
	fmt.Fprintf(&b, "📥 %s — cópia iniciada\n", ev.SlotAlias)
	fmt.Fprintf(&b, "Cartão: %s\n", ev.CardAlias)
	fmt.Fprintf(&b, "%d arquivos (%s) a copiar", ev.FilesTotal, fmtBytes(ev.BytesTotal))
	if ev.FilesSkipped > 0 {
		fmt.Fprintf(&b, " · %d já ingeridos (dedup)", ev.FilesSkipped)
	}
	fmt.Fprintf(&b, "\nInício: %s", in.At.Local().Format("15:04:05"))
	return b.String()
}

func msgProgress(in ProgressInfo) string {
	ev := in.Ev
	pct := 0
	if ev.BytesTotal > 0 {
		pct = int(ev.BytesCopied * 100 / ev.BytesTotal)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "📥 %s — copiando…\n", ev.SlotAlias)
	fmt.Fprintf(&b, "Cartão: %s\n", ev.CardAlias)
	fmt.Fprintf(&b, "%s %d%%\n", progressBar(pct), pct)
	fmt.Fprintf(&b, "%d/%d arquivos · %s de %s",
		ev.FilesCopied, ev.FilesTotal, fmtBytes(ev.BytesCopied), fmtBytes(ev.BytesTotal))
	if ev.FilesFailed > 0 {
		fmt.Fprintf(&b, "\n⚠ %d arquivos com falha até agora", ev.FilesFailed)
	}
	return b.String()
}

func msgCompleted(in CompletedInfo) string {
	ev := in.Ev
	var b strings.Builder
	if ev.FilesFailed > 0 {
		fmt.Fprintf(&b, "⚠ %s — cópia concluída com falhas\n", ev.SlotAlias)
	} else {
		fmt.Fprintf(&b, "✅ %s — cópia concluída\n", ev.SlotAlias)
	}
	fmt.Fprintf(&b, "Cartão: %s\n", ev.CardAlias)
	if in.StatsLine != "" {
		fmt.Fprintf(&b, "%s\n", in.StatsLine)
	}
	if ev.FilesSkipped > 0 {
		fmt.Fprintf(&b, "%d arquivos pulados (já ingeridos)\n", ev.FilesSkipped)
	}
	if ev.FilesFailed > 0 {
		fmt.Fprintf(&b, "❌ %d arquivos falharam após as tentativas — veja o histórico\n", ev.FilesFailed)
	}
	fmt.Fprintf(&b, "Duração %s · %s/s", in.Duration, in.Throughput)
	return b.String()
}

func captionCompleted(in CompletedInfo) string {
	ev := in.Ev
	if ev.FilesFailed > 0 {
		return fmt.Sprintf("⚠ %s: %d arquivos falharam. NÃO remova o cartão do %s antes de verificar.",
			ev.CardAlias, ev.FilesFailed, ev.SlotAlias)
	}
	return fmt.Sprintf("✅ Pode remover o cartão do %s", ev.SlotAlias)
}

func msgFailed(in FailInfo) string {
	ev := in.Ev
	var b strings.Builder
	fmt.Fprintf(&b, "❌ %s — %s\n", ev.SlotAlias, ev.Error)
	fmt.Fprintf(&b, "Cartão: %s\n", ev.CardAlias)
	fmt.Fprintf(&b, "Copiados %d/%d arquivos (%s de %s) antes da falha.",
		ev.FilesCopied, ev.FilesTotal, fmtBytes(ev.BytesCopied), fmtBytes(ev.BytesTotal))
	if strings.Contains(ev.Error, "removido") {
		b.WriteString("\nReinsira o cartão para retomar do ponto de parada (dedup).")
	}
	return b.String()
}

func msgUnknown(in bus.CardUnknown) string {
	label := in.Label
	if label == "" {
		label = "(sem label)"
	}
	return fmt.Sprintf("❓ Cartão desconhecido no %s\nLabel: %s · Serial: %s\nO que fazer?",
		in.SlotAlias, label, in.Serial)
}

func msgDestMissing(in bus.DestMissing) string {
	return fmt.Sprintf("⚠ SSD de destino ausente!\nO cartão %s (%s) está aguardando. "+
		"Conecte o SSD de destino para a cópia iniciar automaticamente.",
		in.CardAlias, in.SlotAlias)
}

func msgTest() string {
	return fmt.Sprintf("✅ cardpit conectado! (%s)", time.Now().Local().Format("02/01/2006 15:04:05"))
}

func progressBar(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct / 10
	return strings.Repeat("▓", filled) + strings.Repeat("░", 10-filled)
}

func fmtBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// statsLine formats the per-type summary for the completion message.
func statsLine(stats map[string]report.TypeStat) string {
	labels := map[string]string{"photo": "fotos", "video": "vídeos", "other": "outros"}
	out := ""
	for _, mt := range []string{"photo", "video", "other"} {
		s, ok := stats[mt]
		if !ok || s.Count == 0 {
			continue
		}
		if out != "" {
			out += " · "
		}
		out += fmt.Sprintf("%d %s (%s)", s.Count, labels[mt], fmtBytes(s.Bytes))
	}
	if out == "" {
		out = "nenhum arquivo novo (tudo já estava ingerido)"
	}
	return out
}

func durThroughput(j store.Job) (string, string) {
	start, err1 := time.Parse(time.RFC3339, j.StartedAt)
	end, err2 := time.Parse(time.RFC3339, j.FinishedAt)
	if err1 != nil || err2 != nil || !end.After(start) {
		return "—", "—"
	}
	d := end.Sub(start).Round(time.Second)
	if d <= 0 {
		d = time.Second
	}
	return d.String(), fmtBytes(int64(float64(j.BytesCopied) / d.Seconds()))
}
