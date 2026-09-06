// Command partner — администрирование Partner API из терминала.
//
// Ключи не выдаются через сам API намеренно: первый ключ выдавать было бы
// нечем, а ручка «создай себе ключ» — это ручка «создай себе доступ». Пока
// партнёров единицы, терминал администратора и есть кабинет.
//
//	go run ./cmd/partner create --slug gorod --name "Агентство «Город»"
//	go run ./cmd/partner key --slug gorod --scopes search:read,leads:read
//	go run ./cmd/partner keys --slug gorod
//	go run ./cmd/partner revoke --prefix hab_live_k4m2rq7t
//	go run ./cmd/partner suspend --slug gorod
//	go run ./cmd/partner list
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
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
	fmt.Fprint(os.Stderr, `Администрирование Partner API.

  create   --slug S --name N [--rpm N] [--llm N]   завести партнёра
  key      --slug S --scopes A,B [--name N] [--env live|test] [--days N]
                                                   выпустить ключ
  keys     --slug S                                показать ключи партнёра
  revoke   --prefix hab_live_…                     отозвать ключ
  suspend  --slug S                                приостановить доступ
  resume   --slug S                                вернуть доступ
  list                                             все партнёры

Скоупы: `+strings.Join(service.AllScopes, ", ")+`
`)
}

func run(command string, args []string) error {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, cfg.DBDSN, 2)
	if err != nil {
		return fmt.Errorf("подключение к базе: %w", err)
	}
	defer pool.Close()

	repo := repository.NewPartnerRepo(pool)
	svc := service.NewPartnerService(repo, repository.NewUserRepo(pool))

	switch command {
	case "create":
		return createPartner(ctx, svc, args)
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
	default:
		usage()
		return errors.New("неизвестная команда: " + command)
	}
}

func createPartner(ctx context.Context, svc *service.PartnerService, args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	slug := fs.String("slug", "", "короткий идентификатор партнёра")
	name := fs.String("name", "", "название компании")
	rpm := fs.Int("rpm", 0, "индивидуальный потолок запросов в минуту (0 — общий из конфига)")
	llm := fs.Int("llm", 0, "индивидуальный потолок вызовов модели в час (0 — общий из конфига)")
	_ = fs.Parse(args)
	if *slug == "" || *name == "" {
		return errors.New("нужны --slug и --name")
	}

	partner, err := svc.Provision(ctx, *slug, *name, optional(*rpm), optional(*llm))
	if err != nil {
		return err
	}
	fmt.Printf("партнёр %s (%s)\nid: %s\nслужебный аккаунт: %s\n",
		partner.Name, partner.Slug, partner.ID, partner.UserID)
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
	days := fs.Int("days", 0, "срок жизни в днях (0 — бессрочный)")
	_ = fs.Parse(args)
	if *slug == "" || *scopes == "" {
		return errors.New("нужны --slug и --scopes")
	}

	partner, err := svc.GetBySlug(ctx, *slug)
	if err != nil {
		return err
	}
	ttl := time.Duration(*days) * 24 * time.Hour
	key, secret, err := svc.IssueKey(ctx, partner.ID, *name, *env,
		strings.Split(*scopes, ","), ttl)
	if err != nil {
		return err
	}

	fmt.Printf("ключ выпущен для %s\nправа: %s\n\n  %s\n\n",
		partner.Slug, strings.Join(key.Scopes, ", "), secret)
	// Секрет в базе не хранится: показать его второй раз не сможем ни мы, ни
	// партнёр. Предупреждение здесь, а не в документации, потому что читают
	// его именно в этот момент.
	fmt.Println("Сохраните ключ сейчас — второй раз он не покажется.")
	return nil
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
	fmt.Fprintln(w, "ПРЕФИКС\tСРЕДА\tСОСТОЯНИЕ\tПРАВА\tПОСЛЕДНИЙ ВЫЗОВ")
	for _, k := range keys {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", k.Prefix, k.Environment,
			keyState(k), strings.Join(k.Scopes, ","), stamp(k.LastUsedAt))
	}
	return w.Flush()
}

func keyState(k domain.APIKey) string {
	switch {
	case k.RevokedAt != nil:
		return "отозван"
	case k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()):
		return "истёк"
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
	prefix := fs.String("prefix", "", "открытая часть ключа")
	_ = fs.Parse(args)
	if *prefix == "" {
		return errors.New("нужен --prefix")
	}
	if err := svc.RevokeKey(ctx, *prefix); err != nil {
		return err
	}
	fmt.Println("ключ отозван:", *prefix)
	return nil
}

func setStatus(ctx context.Context, svc *service.PartnerService, args []string, status string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	slug := fs.String("slug", "", "партнёр")
	_ = fs.Parse(args)
	if *slug == "" {
		return errors.New("нужен --slug")
	}
	partner, err := svc.GetBySlug(ctx, *slug)
	if err != nil {
		return err
	}
	if err := svc.SetStatus(ctx, partner.ID, status); err != nil {
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
