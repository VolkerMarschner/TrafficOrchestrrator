# ============================================================
# Traffic Profile: 3-Tier Web Application — Database Tier
#
# The database tier is the innermost layer:
#   - Accepts connections from the application tier
#   - Primary → replica streaming replication (group:db_tier)
#   - Backup agent outbound to dedicated backup infrastructure
#   - Monitoring / slow-query metrics pushed to group:logging
#
# Tag database tier nodes with  #tag:db_tier  in [TARGETS].
# ============================================================

[META]
NAME        = db_tier
DESCRIPTION = 3-Tier Web Application — Database Tier
VERSION     = 1.0
TAGS        = web, 3tier, database

[RULES]
# PROTO   ROLE     SRC   DST               PORT   INTV  CNT  #name

# --- Inbound from application tier ---
TCP       listen   SELF  -                 5432   -     -    #postgres-listener
TCP       listen   SELF  -                 3306   -     -    #mysql-listener
TCP       listen   SELF  -                 1433   -     -    #mssql-listener

# --- Primary → Replica streaming replication ---
TCP       listen   SELF  -                 5433   -     -    #postgres-replication-listen
TCP       connect  SELF  group:db_tier     5433   30    2    #postgres-replication-connect

# --- Backup agent ---
TCP       connect  SELF  group:logging     9000   300   1    #backup-target

# --- Database metrics / monitoring ---
TCP       listen   SELF  -                 9187   -     -    #postgres-exporter
TCP       connect  SELF  group:logging     514    60    1    #db-syslog

# --- NTP ---
UDP       connect  SELF  ANY               123    60    1    #ntp-sync
