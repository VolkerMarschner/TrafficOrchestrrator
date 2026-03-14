# ============================================================
# Traffic Profile: SAP Application Server (ABAP Stack)
#
# Covers the typical port surface of an SAP NetWeaver ABAP
# application server instance 00:
#   - SAP Message Server (3600/3900)
#   - SAP Dispatcher / ICM (3200 HTTP, 3300 DIAG, 44300 HTTPS)
#   - SAP Gateway (3300 range)
#   - RFC / JCo calls outbound to group:sap_db (database layer)
#   - DB connection to group:sap_db on 1433 (MSSQL) / 1521 (Oracle)
#   - SAP Solution Manager heartbeat (outbound to group:sap_solman)
#
# Tag application servers with  #tag:sap_app  in [TARGETS].
# Tag the database layer with   #tag:sap_db.
# Tag Solution Manager with     #tag:sap_solman.
# ============================================================

[META]
NAME        = sap_application_server
DESCRIPTION = SAP NetWeaver ABAP Application Server (instance 00)
VERSION     = 1.0
TAGS        = sap, erp, abap, netweaver

[RULES]
# PROTO   ROLE     SRC   DST               PORT   INTV  CNT  #name

# --- SAP Message Server ---
TCP       listen   SELF  -                 3600   -     -    #sap-msg-server
TCP       listen   SELF  -                 3900   -     -    #sap-msg-server-http

# --- SAP Dispatcher (DIAG protocol / SAP GUI) ---
TCP       listen   SELF  -                 3200   -     -    #sap-diag
TCP       listen   SELF  -                 3300   -     -    #sap-gateway

# --- SAP ICM (HTTP/HTTPS for Fiori, WebGUI) ---
TCP       listen   SELF  -                 8000   -     -    #sap-icm-http
TCP       listen   SELF  -                 44300  -     -    #sap-icm-https

# --- SAP Router (inbound from external clients) ---
TCP       listen   SELF  -                 3299   -     -    #sap-router

# --- Database connectivity (ABAP → DB layer) ---
TCP       connect  SELF  group:sap_db      1433   60    2    #sap-mssql
TCP       connect  SELF  group:sap_db      1521   60    2    #sap-oracle
TCP       connect  SELF  group:sap_db      3306   60    2    #sap-mysql

# --- RFC / JCo inter-system calls ---
TCP       connect  SELF  group:sap_app     3300   30    3    #sap-rfc-outbound
TCP       connect  SELF  group:sap_app     3600   30    2    #sap-ms-registration

# --- SAP Solution Manager diagnostic agent ---
TCP       connect  SELF  group:sap_solman  9000   120   1    #sap-solman-heartbeat
TCP       connect  SELF  group:sap_solman  9010   300   1    #sap-solman-diag

# --- NTP (time sync is critical for SAP) ---
UDP       connect  SELF  ANY               123    60    1    #ntp-sync
