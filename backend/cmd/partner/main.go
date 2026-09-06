// Command partner — выдача и отзыв доступа к Partner API.
//
// Доступ выдаётся только отсюда. Ручки «создай себе ключ» в API нет и не
// будет: это ручка «создай себе доступ». Но одного доступа к базе тоже
// недостаточно — каждое изменяющее действие требует админ-токена, знает имя
// оператора и оставляет запись в журнале.
//
//	go run ./cmd/partner create  --slug gorod --name "Агентство «Город»"
//	go run ./cmd/partner approve --slug gorod --reason "договор №14 от 2026-09-05"
//	go run ./cmd/partner key     --slug gorod --scopes search:read,leads:read \
//	                             --allow-ip 203.0.113.10,198.51.100.0/24 \
//	                             --reason "боевая интеграция CRM"
//	go run ./cmd/partner keys    --slug gorod
//	go run ./cmd/partner revoke  --slug gorod --prefix hab_live_… --reason "ключ утёк"
//	go run ./cmd/partner log     --slug gorod
package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strings"
	"text/tabwriter"
	"time"

	"habitus-backend/internal/config"
	"habitus-backend/internal/db"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
	"habitus-backend/internal/service"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Выдача доступа к Partner API.

  create   --slug S --name N [--rpm N] [--llm N] [--reason R]
                                          завести партнёра (состояние pending)
  approve  --slug S --reason R            открыть доступ
  key      --slug S --scopes A,B --reason R [--name N] [--env live|test]
           [--allow-ip A,B] [--days N | --forever]
                                          выпустить ключ
  keys     --slug S                       ключи партнёра
  revoke   --slug S --prefix P --reason R отозвать ключ
  suspend  --slug S --reason R            приостановить доступ
  resume   --slug S --reason R            вернуть доступ
  list                                    все партнёры
  log      [--slug S] [--limit N]         журнал выдачи доступа
  hash-token                              посчитать PARTNER_ADMIN_TOKEN_HASH

Изменяющие команды требуют админ-токен в HABITUS_ADMIN_TOKEN — не передавайте
его флагом, флаг попадёт в историю оболочки. Имя оператора берётся из
HABITUS_OPERATOR, иначе из имени пользователя ОС.

Скоупы: `+strings.Join(service.AllScopes, ", ")+`
`)
}

// mutating — команды, которые меняют доступ. Чтение оставлено открытым: оно
// и так требует доступа к базе, а требовать токен ради `keys` значит сделать
// диагностику в инциденте сложнее, чем выдачу ключа.
var mutating = map[string]bool{
	"create": true, "approve": true, "key": true,
	"revoke": true, "suspend": true, "resume": true,
}

func run(command string, args []string) error {
	if command == "hash-token" {
		return hashToken()
	}

	cfg := config.Load()
	if mutating[command] {
		if err := authorize(cfg); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, cfg.DBDSN, 2)
	if err != nil {
		return fmt.Errorf("подключение к базе: %w", err)
	}
	defer pool.Close()

	repo := repository.NewPartnerRepo(pool)
	svc := service.NewPartnerService(repo, repository.NewUserRepo(pool), cfg.PartnerKeyPepper)

	switch command {
	case "create":
		return createPartner(ctx, svc, args)
	case "approve":
		return approvePartner(ctx, svc, args)
	case "key":
		return issueKey(ctx, svc, args)
	case "keys":
		return listKeys(ctx, svc, args)
	case "revoke":
		return revokeKey(ctx, svc, args)
	case "suspend":
		return setStatus(ctx, svc, args, domain.PartnerStatusSuspended)
	case "resume":
		return setStatus(ctx, svc, args, domain.PartnerStatusActive)
	case "list":
		return listPartners(ctx, svc)
	case "log":
		return showLog(ctx, svc, args)
	default:
		usage()
		return errors.New("неизвестная команда: " + command)
	}
}

// authorize сверяет предъявленный токен с хешем из конфига сервера.
//
// Хеш, а не сам токен: чтение конфига не должно давать право выдавать доступ.
// Это не защита от того, у кого есть машина целиком (перец для ключей всё
// равно лежит рядом) — это защита от случайной выдачи, от чужих рук на
// открытой сессии и, главное, привязка каждого действия к живому человеку в
// журнале.
func authorize(cfg config.Settings) error {
	if cfg.PartnerAdminTokenHash == "" {
		return errors.New("PARTNER_ADMIN_TOKEN_HASH не задан на сервере — " +
			"выдача доступа выключена. Посчитайте хеш: partner hash-token")
	}
	token := strings.TrimSpace(os.Getenv("HABITUS_ADMIN_TOKEN"))
	if token == "" {
		return errors.New("нужен админ-токен в HABITUS_ADMIN_TOKEN")
	}
	sum := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])),
		[]byte(strings.TrimSpace(cfg.PartnerAdminTokenHash))) != 1 {
		return errors.New("админ-токен не подошёл")
	}
	return nil
}

func hashToken() error {
	token := strings.TrimSpace(os.Getenv("HABITUS_ADMIN_TOKEN"))
	if token == "" {
		return errors.New("положите токен в HABITUS_ADMIN_TOKEN — флагом его " +
			"передавать нельзя, он попадёт в историю оболочки")
	}
	sum := sha256.Sum256([]byte(token))
	fmt.Printf("PARTNER_ADMIN_TOKEN_HASH=%s\n", hex.EncodeToString(sum[:]))
	return nil
}

// operator — кто именно выполняет действие. Журнал без имени человека
// отвечает на вопрос «что произошло», но не на вопрос «с кого спросить».
func operator() string {
	if name := strings.TrimSpace(os.Getenv("HABITUS_OPERATOR")); name != "" {
		return name
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		host, _ := os.Hostname()
		if host != "" {
			return u.Username + "@" + host
		}
		return u.Username
	}
	return "unknown"
}

// requireReason. Причина обязательна у всего, что меняет доступ: строка
// журнала без неё говорит «кто-то выдал ключ» и на этом заканчивается.
func requireReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", errors.New("нужен --reason: зачем выдаётся или отзывается доступ")
	}
	return reason, nil
}

func createPartner(ctx context.Context, svc *service.PartnerService, args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	slug := fs.String("slug", "", "короткий идентификатор партнёра")
	name := fs.String("name", "", "название компании")
	rpm := fs.Int("rpm", 0, "индивидуальный потолок запросов в минуту (0 — общий из конфига)")
	llm := fs.Int("llm", 0, "индивидуальный потолок вызовов модели в час (0 — общий из конфига)")
	reason := fs.String("reason", "", "зачем заводится партнёр")
	_ = fs.Parse(args)
	if *slug == "" || *name == "" {
		return errors.New("нужны --slug и --name")
	}

	partner, err := svc.Provision(ctx, *slug, *name, optional(*rpm), optional(*llm),
		operator(), strings.TrimSpace(*reason))
	if err != nil {
		return err
	}
	fmt.Printf("партнёр %s (%s)\nсостояние: %s\nid: %s\nслужебный аккаунт: %s\n",
		partner.Name, partner.Slug, partner.Status, partner.ID, partner.UserID)
	if partner.Status != domain.PartnerStatusActive {
		fmt.Printf("\nДоступа пока нет. Открыть:\n  partner approve --slug %s --reason \"…\"\n",
			partner.Slug)
	}
	return nil
}

func approvePartner(ctx context.Context, svc *service.PartnerService, args []string) error {
	fs := flag.NewFlagSet("approve", flag.ExitOnError)
	slug := fs.String("slug", "", "партнёр")
	reasonFlag := fs.String("reason", "", "основание: номер договора, задача, письмо")
	_ = fs.Parse(args)
	if *slug == "" {
		return errors.New("нужен --slug")
	}
	reason, err := requireReason(*reasonFlag)
	if err != nil {
		return err
	}
	partner, err := svc.Approve(ctx, *slug, operator(), reason)
	if err != nil {
		return err
	}
	fmt.Printf("%s: доступ открыт (%s)\n", partner.Slug, partner.Status)
	return nil
}

// optional превращает 0 в nil: ноль в конфиге партнёра означал бы «ничего
// нельзя», а имелось в виду «лимит не назначали».
func optional(v int) *int {
	if v <= 0 {
		return nil
	}
	return &v
}

func issueKey(ctx context.Context, svc *service.PartnerService, args []string) error {
	fs := flag.NewFlagSet("key", flag.ExitOnError)
	slug := fs.String("slug", "", "партнёр")
	name := fs.String("name", "", "как назвать ключ")
	env := fs.String("env", "live", "среда: live или test")
	scopes := fs.String("scopes", "", "права через запятую")
	allowIP := fs.String("allow-ip", "", "адреса и подсети через запятую; пусто — откуда угодно")
	days := fs.Int("days", 0, "срок жизни в днях (0 — по умолчанию 180)")
	forever := fs.Bool("forever", false, "бессрочный ключ — только если иначе никак")
	reasonFlag := fs.String("reason", "", "зачем выдаётся ключ")
	_ = fs.Parse(args)
	if *slug == "" || *scopes == "" {
		return errors.New("нужны --slug и --scopes")
	}
	reason, err := requireReason(*reasonFlag)
	if err != nil {
		return err
	}

	partner, err := svc.GetBySlug(ctx, *slug)
	if err != nil {
		return err
	}
	key, secret, err := svc.IssueKey(ctx, service.IssueKeyInput{
		Partner: partner, Name: *name, Environment: *env,
		Scopes:     splitList(*scopes),
		AllowedIPs: splitList(*allowIP),
		TTL:        time.Duration(*days) * 24 * time.Hour,
		Forever:    *forever,
		Operator:   operator(), Reason: reason,
	})
	if err != nil {
		return err
	}

	fmt.Printf("ключ выпущен для %s\nправа: %s\nадреса: %s\nдействует до: %s\n\n  %s\n\n",
		partner.Slug, strings.Join(key.Scopes, ", "), addrs(key.AllowedIPs), until(key.ExpiresAt), secret)
	// Секрет в базе не хранится: показать его второй раз не сможем ни мы, ни
	// партнёр. Предупреждение здесь, а не в документации, потому что читают
	// его именно в этот момент.
	fmt.Println("Сохраните ключ сейчас — второй раз он не покажется.")
	if len(key.AllowedIPs) == 0 {
		fmt.Println("Адреса не ограничены: утёкший ключ будет работать откуда угодно.")
	}
	return nil
}

func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func addrs(v []string) string {
	if len(v) == 0 {
		return "любые"
	}
	return strings.Join(v, ", ")
}

func until(t *time.Time) string {
	if t == nil {
		return "бессрочно"
	}
	return t.Format("2006-01-02")
}

func listKeys(ctx context.Context, svc *service.PartnerService, args []string) error {
	fs := flag.NewFlagSet("keys", flag.ExitOnError)
	slug := fs.String("slug", "", "партнёр")
	_ = fs.Parse(args)
	if *slug == "" {
		return errors.New("нужен --slug")
	}
	partner, err := svc.GetBySlug(ctx, *slug)
	if err != nil {
		return err
	}
	keys, err := svc.ListKeys(ctx, partner.ID)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ПРЕФИКС\tСРЕДА\tСОСТОЯНИЕ\tДО\tАДРЕСА\tПРАВА\tПОСЛЕДНИЙ ВЫЗОВ")
	for _, k := range keys {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", k.Prefix, k.Environment,
			keyState(k), until(k.ExpiresAt), addrs(k.AllowedIPs),
			strings.Join(k.Scopes, ","), stamp(k.LastUsedAt))
	}
	return w.Flush()
}

func keyState(k domain.APIKey) string {
	switch {
	case k.RevokedAt != nil:
		return "отозван"
	case k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()):
		return "истёк"
	case k.ExpiresAt == nil:
		// Бессрочный ключ помечается отдельно: его никто не отзовёт по
		// расписанию, и в списке это должно бросаться в глаза.
		return "бессрочный"
	default:
		return "рабочий"
	}
}

func stamp(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.Format("2006-01-02 15:04")
}

func revokeKey(ctx context.Context, svc *service.PartnerService, args []string) error {
	fs := flag.NewFlagSet("revoke", flag.ExitOnError)
	slug := fs.String("slug", "", "партнёр")
	prefix := fs.String("prefix", "", "открытая часть ключа")
	reasonFlag := fs.String("reason", "", "почему отзывается")
	_ = fs.Parse(args)
	if *slug == "" || *prefix == "" {
		return errors.New("нужны --slug и --prefix")
	}
	reason, err := requireReason(*reasonFlag)
	if err != nil {
		return err
	}
	partner, err := svc.GetBySlug(ctx, *slug)
	if err != nil {
		return err
	}
	if err := svc.RevokeKey(ctx, partner, *prefix, operator(), reason); err != nil {
		return err
	}
	fmt.Println("ключ отозван:", *prefix)
	return nil
}

func setStatus(ctx context.Context, svc *service.PartnerService, args []string, status string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	slug := fs.String("slug", "", "партнёр")
	reasonFlag := fs.String("reason", "", "основание")
	_ = fs.Parse(args)
	if *slug == "" {
		return errors.New("нужен --slug")
	}
	reason, err := requireReason(*reasonFlag)
	if err != nil {
		return err
	}
	partner, err := svc.GetBySlug(ctx, *slug)
	if err != nil {
		return err
	}
	if err := svc.SetStatus(ctx, partner, status, operator(), reason); err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", partner.Slug, status)
	return nil
}

func listPartners(ctx context.Context, svc *service.PartnerService) error {
	partners, err := svc.List(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tНАЗВАНИЕ\tСОСТОЯНИЕ\tЗАВЕДЁН")
	for _, p := range partners {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Slug, p.Name, p.Status,
			p.CreatedAt.Format("2006-01-02"))
	}
	return w.Flush()
}

func showLog(ctx context.Context, svc *service.PartnerService, args []string) error {
	fs := flag.NewFlagSet("log", flag.ExitOnError)
	slug := fs.String("slug", "", "партнёр; пусто — все")
	limit := fs.Int("limit", 50, "сколько записей показать")
	_ = fs.Parse(args)

	entries, err := svc.AdminLog(ctx, strings.TrimSpace(*slug), *limit)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "КОГДА\tОПЕРАТОР\tДЕЙСТВИЕ\tПАРТНЁР\tКЛЮЧ\tОСНОВАНИЕ")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			e.CreatedAt.Format("2006-01-02 15:04"), e.Operator, e.Action,
			e.Slug, dash(e.KeyPrefix), dash(e.Reason))
	}
	return w.Flush()
}

func dash(v string) string {
	if v == "" {
		return "—"
	}
	return v
}
