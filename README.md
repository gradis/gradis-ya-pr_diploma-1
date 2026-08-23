# Gophermart

HTTP API накопительной системы лояльности из дипломного проекта курса Go-разработчика.

Сервис регистрирует и аутентифицирует пользователей, принимает номера заказов, получает начисления из внешней
системы, ведёт баланс баллов и регистрирует списания. Данные хранятся в PostgreSQL.

## Стек

- Go, Gin;
- PostgreSQL, pgxpool;
- golang-migrate со встроенными в бинарник миграциями;
- Zap;
- bcrypt и подписанная HttpOnly-cookie;
- gzip для входящих запросов и исходящих ответов.

## Конфигурация

| Назначение | Флаг | Переменная окружения |
| --- | --- | --- |
| Адрес HTTP-сервера | `-a` | `RUN_ADDRESS` |
| URI PostgreSQL | `-d` | `DATABASE_URI` |
| Адрес accrual-сервиса | `-r` | `ACCRUAL_SYSTEM_ADDRESS` |
| Секрет подписи сессий | — | `AUTH_SECRET` |

При запуске из корня проекта сервис автоматически читает локальный `.env`. Готовый пример находится в
`.env.example`; настоящий `.env` исключён из Git. Приоритет настроек: переменные окружения ОС, флаги, `.env`,
значения по умолчанию. Для подписи cookie можно дополнительно передать `AUTH_SECRET`.
Если секрет не задан, при каждом запуске генерируется криптографически стойкий случайный ключ. Для production и запуска
нескольких экземпляров приложения необходимо передавать один постоянный `AUTH_SECRET`, иначе сессии перестанут
действовать после перезапуска или при попадании запроса на другой экземпляр.

## Запуск

Сначала можно поднять локальный PostgreSQL через Docker Compose:

```bash
docker compose up -d postgres
docker compose ps
```

При первом запуске создаются основная база `gophermart` и отдельная база `gophermart_test` для интеграционных тестов.
Данные основной базы сохраняются в именованном Docker volume.

```bash
go build -o gophermart ./cmd/gophermart

DATABASE_URI='postgres://postgres:postgres@localhost:5432/praktikum?sslmode=disable' \
ACCRUAL_SYSTEM_ADDRESS='http://localhost:8081' \
RUN_ADDRESS='localhost:8080' \
AUTH_SECRET='replace-in-production' \
./gophermart
```

Миграции применяются автоматически при старте приложения.

Для локального запуска достаточно скопировать `.env.example` в `.env`, заполнить значения и выполнить:

```bash
go run ./cmd/gophermart
```

## Проверка

```bash
go test ./...
go test -race ./...
go vet ./...
```

Интеграционный тест репозитория по умолчанию пропускается. Для его запуска нужна отдельная тестовая база: тест очищает
таблицы `users`, `orders` и `withdrawals` перед и после проверки.

```bash
TEST_DATABASE_URI='postgres://postgres:postgres@localhost:5432/gophermart_test?sslmode=disable' \
go test -v ./internal/repository/postgres
```

Остановить локальную базу можно командой `docker compose down`. Команда `docker compose down -v` дополнительно удалит
volume вместе со всеми локальными данными PostgreSQL.

Полная спецификация API находится в [SPECIFICATION.md](SPECIFICATION.md).
