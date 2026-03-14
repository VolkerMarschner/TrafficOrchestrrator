# ============================================================
# Traffic Profile: 3-Tier Web Application — Web/Frontend Tier
#
# The web tier (reverse proxy / load balancer / CDN origin)
# sits in the DMZ and:
#   - Accepts inbound HTTP/HTTPS from the internet (ANY)
#   - Forwards requests to the application tier (group:app_tier)
#   - Receives health-check probes from the load balancer
#   - Pushes access logs to a syslog server (group:logging)
#
# Tag web tier nodes with  #tag:web_tier  in [TARGETS].
# ============================================================

[META]
NAME        = web_tier
DESCRIPTION = 3-Tier Web Application — Web / Reverse-Proxy Tier
VERSION     = 1.0
TAGS        = web, 3tier, frontend, dmz

[RULES]
# PROTO   ROLE     SRC   DST               PORT   INTV  CNT  #name

# --- Public inbound (HTTP/HTTPS) ---
TCP       listen   SELF  -                 80     -     -    #http-public
TCP       listen   SELF  -                 443    -     -    #https-public

# --- Forwarding to application tier ---
TCP       connect  SELF  group:app_tier    8080   10    5    #app-forward-http
TCP       connect  SELF  group:app_tier    8443   10    5    #app-forward-https

# --- Load-balancer health check listener ---
TCP       listen   SELF  -                 8880   -     -    #health-check

# --- Syslog / access log forwarding ---
UDP       connect  SELF  group:logging     514    30    1    #syslog
TCP       connect  SELF  group:logging     6514   60    1    #syslog-tls

# --- NTP ---
UDP       connect  SELF  ANY               123    60    1    #ntp-sync
