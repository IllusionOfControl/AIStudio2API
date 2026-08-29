# Мониторинг AIStudio2API (Prometheus + Grafana)

В AIStudio2API встроен полноценный сбор метрик с экспортом в формате Prometheus и готовым дашбордом для Grafana.

---

## 🚀 Быстрый старт

### 1. Запуск AIStudio2API
Запустите сервер как обычно:
```bash
./start.bat
# или
go run ./cmd/aistudio2api
```
Метрики доступны по адресу: **`http://localhost:8080/metrics`**

### 2. Запуск Prometheus и Grafana в Docker
В корне проекта выполните:
```bash
# Windows
start-monitoring.bat

# Linux / macOS
./start-monitoring.sh
# или
docker compose -f docker-compose.metrics.yml up -d
```

### 3. Доступ к панелям
- **Grafana**: [http://localhost:3000](http://localhost:3000)
  - Логин: `admin`
  - Пароль: `admin`
  - Дашборд **"AIStudio2API Overview & Metrics"** преднастроен и загружается автоматически.
- **Prometheus**: [http://localhost:9090](http://localhost:9090)

---

## 📊 Доступные метрики

### 1. HTTP & API запросы
| Метрика | Тип | Описание |
|---|---|---|
| `http_requests_total` | Counter | Количество HTTP запросов по методу (`method`), маршруту (`path`) и статусу (`status`). |
| `http_request_duration_seconds` | Histogram | Длительность HTTP запросов в секундах. |
| `http_requests_in_flight` | Gauge | Текущее количество активных HTTP соединений в обработке. |
| `http_response_size_bytes` | Histogram | Размер HTTP ответов в байтах. |

### 2. Генерация и LLM
| Метрика | Тип | Описание |
|---|---|---|
| `aistudio2api_generation_requests_total` | Counter | Количество запросов генерации по модели (`model`), протоколу (`protocol`: openai, anthropic, gemini), типу стриминга (`stream`) и статусу (`status`). |
| `aistudio2api_generation_duration_seconds` | Histogram | Полная задержка генерации ответа в секундах (E2E Latency). |
| `aistudio2api_generation_time_to_first_token_seconds` | Histogram | Время до генерации первого токена контента (**TTFT**). |
| `aistudio2api_generation_time_to_first_event_seconds` | Histogram | Время до получения первого события от апстрима Google. |
| `aistudio2api_generation_active_requests` | Gauge | Количество активных генераций в реальном времени. |
| `aistudio2api_generation_tokens_total` | Counter | Потребление токенов с разбивкой по типу (`type`: prompt, completion, reasoning), модели и аккаунту. |
| `aistudio2api_generation_chars_total` | Counter | Количество сгенерированных символов текста. |
| `aistudio2api_generation_finish_reasons_total` | Counter | Причины завершения стрима (`STOP`, `MAX_TOKENS`, `SAFETY`, `TOOL_CALLS` и др.). |

### 3. Оркестратор и апстрим
| Метрика | Тип | Описание |
|---|---|---|
| `aistudio2api_orchestrator_preparation_duration_seconds` | Histogram | Время фаз подготовки (генерация WAA пруфа, ожидание заголовков ответа). |
| `aistudio2api_orchestrator_upstream_bytes_total` | Counter | Сетевой трафик с апстримом (байты получено/отправлено). |
| `aistudio2api_orchestrator_retries_total` | Counter | Количество повторных попыток и переключений аккаунтов при сбоях (Failover). |
| `aistudio2api_orchestrator_stream_stalls_total` | Counter | Количество обнаруженных зависаний/пауз в стриме. |
| `aistudio2api_orchestrator_latest_latency_seconds` | Gauge | Последняя замеренная задержка ответа для пары аккаунт-модель. |

### 4. Пул аккаунтов и воркеры (WAA)
| Метрика | Тип | Описание |
|---|---|---|
| `aistudio2api_accounts_total` | Gauge | Количество аккаунтов по состояниям (`ready`, `busy`, `cooldown`, `auth_required`, `unavailable`). |
| `aistudio2api_accounts_cooldowns_total` | Counter | Количество уходов аккаунтов в кулдаун по моделям и причинам (Rate Limit, Quota и т.д.). |
| `aistudio2api_workers_total` | Gauge | Количество браузерных WAA воркеров по состояниям (`running`, `prewarmed`, `failed`). |
| `aistudio2api_workers_launches_total` | Counter | Количество запусков браузерных воркеров Camoufox и их статус (`success`/`failure`). |
| `aistudio2api_workers_launch_duration_seconds` | Histogram | Длительность холодного старта воркера в секундах. |

### 5. Токенизация и медиа
| Метрика | Тип | Описание |
|---|---|---|
| `aistudio2api_tokens_count_requests_total` | Counter | Запросы подсчета токенов (`/count_tokens`). |
| `aistudio2api_video_requests_total` | Counter | Запросы генерации видео. |

### 6. Системные метрики Go Runtime
| Метрика | Тип | Описание |
|---|---|---|
| `go_goroutines` | Gauge | Количество активных горутин в процессе. |
| `go_memstats_alloc_bytes` | Gauge | Выделенная память кучи (Heap). |
| `process_cpu_seconds_total` | Counter | Загрузка CPU процессом. |
| `go_gc_duration_seconds` | Summary | Время пауз сборщика мусора GC. |

---

## 📈 Вид дашборда Grafana

Дашборд **"AIStudio2API Overview & Metrics"** включает следующие секции:
1. **Overview & Key Stats**: активные запросы, общий объем генераций, текущий RPS, % ошибок, общее число токенов, медианный TTFT (P50).
2. **Request Rates & Traffic Analysis**: графики RPS с разбивкой по моделям (Gemini, Claude, GPT-совместимые), протоколам (OpenAI / Anthropic / Gemini) и статус-кодам HTTP (200, 4xx, 5xx).
3. **Latency, TTFT & Performance**: перцентили задержки (P50, P90, P95, P99), TTFT по моделям и детальное время фаз подготовки (WAA Proof).
4. **Token Usage & Generation Analytics**: скорость генерации токенов/сек (Prompt, Completion, Reasoning), круговые диаграммы распределения токенов по моделям и аккаунтам.
5. **Account Pool, Workers & Failovers**: состояние аккаунтов в пуле, события кулдауна и сбоев, перезапуски воркеров, причины остановки стрима.
6. **System & Process Health**: использование памяти (Heap / RSS), горутины, потоки ОС, трафик сети и загрузка CPU.
