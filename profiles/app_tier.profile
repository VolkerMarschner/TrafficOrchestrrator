# ============================================================
# Traffic Profile: 3-Tier Web Application — Application Tier
#
# The application tier sits between the web and database tiers:
#   - Accepts proxied HTTP from the web tier (group:web_tier)
#   - Connects to the database tier for reads/writes (group:db_tier)
#   - Connects to a cache layer (group:cache_tier) for session data
#   - Connects to a message broker (group:mq_tier) for async jobs
#   - Internal service-to-service calls to peers (group:app_tier)
#
# Tag application tier nodes with  #tag:app_tier  in [TARGETS].
# ============================================================

[META]
NAME        = app_tier
DESCRIPTION = 3-Tier Web Application — Application / Business Logic Tier
VERSION     = 1.0
TAGS        = web, 3tier, application, backend

[RULES]
# PROTO   ROLE     SRC   DST               PORT   INTV  CNT  #name

# --- Inbound from web tier ---
TCP       listen   SELF  -                 8080   -     -    #app-http-listener
TCP       listen   SELF  -                 8443   -     -    #app-https-listener

# --- Health check / metrics scrape ---
TCP       listen   SELF  -                 9090   -     -    #metrics-prometheus

# --- Database connectivity ---
TCP       connect  SELF  group:db_tier     5432   30    3    #postgres-primary
TCP       connect  SELF  group:db_tier     3306   30    3    #mysql-primary

# --- Cache layer (Redis / Memcached) ---
TCP       connect  SELF  group:cache_tier  6379   15    4    #redis
TCP       connect  SELF  group:cache_tier  11211  15    2    #memcached

# --- Message broker (RabbitMQ / Kafka) ---
TCP       connect  SELF  group:mq_tier     5672   20    3    #amqp
TCP       connect  SELF  group:mq_tier     9092   20    3    #kafka

# --- Peer app-tier calls (microservice / API mesh) ---
TCP       connect  SELF  group:app_tier    8080   15    2    #internal-api

# --- External identity provider (OIDC / LDAP) ---
TCP       connect  SELF  ANY               443    60    1    #oidc-provider

# --- NTP ---
UDP       connect  SELF  ANY               123    60    1    #ntp-sync
