# PromoHub — promohub

Go hub (`github.com/lioilsources/promohub`), který sbírá metriky a zmínky z promo účtů, pouští pravidla a publikuje.
Repo je **veřejné**: žádné IP, domovské cesty, tokeny ani soukromé údaje (ani ve fixtures).

Plány:

- `Prompts/00-PROMO_HUB_PLAN.md` — celkový plán, fáze 1 je hotová;
- `Prompts/01-ACCOUNTS_PLAN.md` — účty a platformy, vlny 1–5;
- `Prompts/02-CHROME_PROMPTS.md` — ruční kroky přes Claude v Chrome.

Ruční kroky pro provoz jsou v `RUNBOOK.md`, ověření API platforem v `docs/PLATFORMS.md`.

## Gate před pushem

```
make vet test check
make test-db            # TEST_PG_PORT=55439, když je 55432 obsazený
```

## Struktura

- `cmd/promohub` — podpříkazy:
  - `serve`, `migrate`, `check`;
  - `accounts` — stav účtů;
  - `probe <platform:handle> [capability]` — jeden průchod adaptéru proti živému API, bez DB. `PROBE_RAW=1` vypíše surové odpovědi pro fixtures.
- `internal/platform` — registr platforem. Jediné místo pro limity, cesty `api|apify|ifttt|manual|none`, intervaly a `ExternalID`.
- `internal/platforms/<name>` — adaptéry: klient, `Collector`, později `Publisher`. Sdílený HTTP klient je `httpx`.
- `internal/collectors` — neutrální model (`Batch`, `Snapshot`, `Inbound`) a rozhraní `Collector`. `apifysrc` mapuje Apify datasety.
- `internal/hub` — webhooky, `Ingest` (jediné místo, které ukládá), plánovač (`collect.go`), outbox.
- `internal/creds` — statické secrets z env podle `auth_ref`.
- `internal/store` — migrace a sqlc. Po změně `query.sql` spusť `make generate`; vygenerovaný kód needituj.

## Pravidla

- Adaptér nezná DB. Vrací `Batch` a kurzor, ukládá jen `hub.Ingest`.
- Nasazenou migraci needituj, přidej novou.
- Testy bez sítě a bez `time.Now`. Klienti mají `Base`/`BaseURL` pro `httptest`, hub má `now` a `rand`.
- Každé mapování má fixture z reálné odpovědi (zkrácenou, bez tokenů, cizí účty anonymizované).
- Žádný Python ani Bash skripty, jen Go a Makefile.
- Nic se nepublikuje bez schválení v Telegramu (vlna 2+).
