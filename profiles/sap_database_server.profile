# ============================================================
# Traffic Profile: SAP Database Server
#
# Covers the database tier of an SAP landscape:
#   - Listens for ABAP application server connections
#   - SAP HANA (3xx13 / 3xx15) or traditional RDBMS ports
#   - Replication to a standby DB node (group:sap_db)
#   - Backup agent outbound to group:sap_backup
#
# Tag database servers with  #tag:sap_db  in [TARGETS].
# ============================================================

[META]
NAME        = sap_database_server
DESCRIPTION = SAP Database Server (HANA / MSSQL / Oracle)
VERSION     = 1.0
TAGS        = sap, database, hana

[RULES]
# PROTO   ROLE     SRC   DST               PORT   INTV  CNT  #name

# --- SAP HANA SQL/MDX (instance 00) ---
TCP       listen   SELF  -                 30013  -     -    #hana-sql
TCP       listen   SELF  -                 30015  -     -    #hana-indexserver

# --- SAP HANA system replication ---
TCP       listen   SELF  -                 40000  -     -    #hana-sysrep-listen
TCP       connect  SELF  group:sap_db      40000  30    2    #hana-sysrep-connect

# --- Traditional RDBMS fallback ports ---
TCP       listen   SELF  -                 1433   -     -    #mssql-listener
TCP       listen   SELF  -                 1521   -     -    #oracle-listener
TCP       listen   SELF  -                 3306   -     -    #mysql-listener

# --- Backup agent (outbound to backup infrastructure) ---
TCP       connect  SELF  group:sap_backup  9000   300   1    #backup-agent
TCP       connect  SELF  group:sap_backup  6500   300   1    #tsm-backup

# --- NTP ---
UDP       connect  SELF  ANY               123    60    1    #ntp-sync
