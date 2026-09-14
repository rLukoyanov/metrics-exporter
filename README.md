# metrics-generator

Веб-приложение, которое отправляет произвольные метрики в Prometheus через
[Pushgateway](https://github.com/prometheus/pushgateway). Состоит из Go-бэкенда
и Svelte UI (встроен в бинарник). Рассчитано на запуск в Kubernetes: токен
service account читается из projected volume и передаётся в заголовке
`Authorization: Bearer <token>`.

## Возможности

- Типы метрик: `gauge`, `counter`, `histogram`, `summary`.
- Произвольные labels и grouping labels (они попадают в путь Pushgateway).
- Для histogram `+Inf`-бакет добавляется автоматически; `sum`/`count` опциональны.
- Два транспорта: Pushgateway и Remote Write (`/api/v1/write`), переключаются в UI.
- Периодическая отправка: в UI есть галочка «Писать периодически (loop)» —
  интервал в секундах и опциональная продолжительность (пусто = бесконечно),
  кнопка «Стоп».
- SA-токен перечитывается на каждый запрос (совместимо с ротацией projected token).
- Один статический бинарник со встроенным UI.

## Как это работает

Prometheus — pull-система, поэтому поддерживаются два транспорта отправки
(переключаются в UI, либо задаются через `SEND_MODE`):

1. **Pushgateway** (по умолчанию):

```
UI -> POST /api/push -> Go backend -> PUT {PUSHGATEWAY_URL}/metrics/job/<job>/<grouping labels>
```

2. **Remote write** (`/api/v1/write`) — отправка прямо в TSDB Prometheus
   (snappy-сжатый protobuf `prompb.WriteRequest`):

```
UI -> POST /api/push -> Go backend -> POST {REMOTE_WRITE_URL}/api/v1/write
```

Для приёма remote write на сервере Prometheus должен быть включён
`--web.enable-remote-write-receiver`. Для histogram бакеты конвертируются в
классические серии `name_bucket{le=...}`/`_sum`/`_count` — Prometheus собирает их
обратно в гистограмму (работает `histogram_quantile`).

Если Prometheus/Pushgateway закрыт аутентификацией (kube-rbac-proxy,
oauth2-proxy и т.п.), бэкенд добавляет `Authorization: Bearer` с содержимым
SA-токена. Если токен не найден, запрос уходит без заголовка.

## Конфигурация (env)

| Переменная | По умолчанию | Описание |
| --- | --- | --- |
| `SEND_MODE` | `pushgateway` | Режим по умолчанию: `pushgateway` или `remotewrite` |
| `PUSHGATEWAY_URL` | `http://localhost:9091` | Базовый URL Pushgateway (или прокси перед ним) |
| `REMOTE_WRITE_URL` | пусто (выключено) | Базовый URL Prometheus (без `/api/v1/write` — добавляется сам) |
| `SA_TOKEN_PATH` | `/var/run/secrets/kubernetes.io/serviceaccount/token` | Путь к SA-токену |
| `LISTEN_ADDR` | `:8080` | Адрес прослушивания |
| `PUSHGATEWAY_TIMEOUT` / `REMOTE_WRITE_TIMEOUT` | `10s` | Таймаут HTTP-запроса |
| `PUSHGATEWAY_SKIP_TLS_VERIFY` / `REMOTE_WRITE_SKIP_TLS_VERIFY` | `false` | `1`/`true` — не проверять TLS-сертификат |

## Локальный запуск

Терминал 1 — бэкенд:

```sh
PUSHGATEWAY_URL=http://localhost:9091 SA_TOKEN_PATH= go run .
```

`SA_TOKEN_PATH=` (пусто) отключает отправку токена локально.

Терминал 2 — UI с hot reload (проксирует `/api` на `:8080`):

```sh
cd web
npm install
npm run dev
```

Для production-сборки UI: `cd web && npm run build`, затем `go build`.

## Docker

```sh
docker build -t metrics-generator:latest .
docker run --rm -p 8080:8080 \
  -e PUSHGATEWAY_URL=http://host.docker.internal:9091 \
  metrics-generator:latest
```

## Автосборка образа (GitHub Actions)

`.github/workflows/build-image.yml` собирает образ при push в `main`/`master` и
пушит его в GitHub Container Registry (логин автоматический, через
`GITHUB_TOKEN`). Сборка мультиархитектурная — `linux/amd64` и `linux/arm64`.

Образ лежит по адресу:

```
ghcr.io/<ваш-аккаунт>/<название-репозитория>:main   # последняя сборка
ghcr.io/<ваш-аккаунт>/<название-репозитория>:sha-<sha>  # по коммиту
```

Скачать и запустить:

```sh
docker pull ghcr.io/<ваш-аккаунт>/<название-репозитория>:main
docker run --rm -p 8080:8080 \
  -e PUSHGATEWAY_URL=http://pushgateway.example:9091 \
  ghcr.io/<ваш-аккаунт>/<название-репозитория>:main
```

> Для локального pull в первый раз выполните `docker login ghcr.io`
> (для публичного пакета можно без логина).

## Полный стек для проверки (docker compose)

Поднимает приложение, Pushgateway, nginx с проверкой Bearer-токена (имитация
kube-rbac-proxy) и Prometheus, который скрейпит Pushgateway и принимает remote
write (`--web.enable-remote-write-receiver`):

```sh
docker compose up -d --build
```

- UI: http://localhost:8080 (в настройках отправки можно выбрать Remote Write)
- Prometheus: http://localhost:9090
- Pushgateway: http://localhost:9091 (требует `Authorization: Bearer demo-sa-token-123`)

Демо-токен лежит в `docker/token`. Prometheus-конфиг — `docker/prometheus.yml`.

> Примечание: Pushgateway использует `PUT` — отправка метрики без grouping labels
> перезаписывает **всю** группу `job`. Разные группы (например, разный `instance`)
> или разные `job` хранятся независимо.

## Kubernetes

```sh
kubectl apply -k deploy
```

В `deploy/deployment.yaml` замените `PUSHGATEWAY_URL` на адрес вашего
Pushgateway/прокси. Токен монтируется автоматически (`automountServiceAccountToken`).
Если перед Pushgateway стоит kube-rbac-proxy, SA `metrics-generator` должен
иметь право на соответствующий ресурс (RBAC-роль и rolebinding).

## API

`POST /api/push` — необязательное поле `mode` (`pushgateway`/`remotewrite`)
переопределяет `SEND_MODE` на уровне запроса.

```json
{
  "mode": "pushgateway",
  "job": "manual",
  "groupingLabels": [{ "name": "instance", "value": "host-1" }],
  "metrics": [
    {
      "name": "requests_total",
      "help": "Total requests",
      "type": "counter",
      "labels": [{ "name": "code", "value": "200" }],
      "value": 42
    }
  ]
}
```

- histogram: `buckets: [{ "le": 0.1, "count": 2 }]` (count кумулятивный), опционально `sum`, `count`.
- summary: `quantiles: [{ "quantile": 0.5, "value": 0.05 }]`, опционально `sum`, `count`.

`GET /api/config` — текущий URL Pushgateway и наличие токена (для UI).
`GET /healthz` — проба живости.
