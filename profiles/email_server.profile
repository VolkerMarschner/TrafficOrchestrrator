# ============================================================
# Traffic Profile: Enterprise Email Server
#
# Covers a typical on-premises mail server (Exchange / Postfix):
#   - Inbound SMTP from internet (port 25) and internal clients
#   - Secure submission (587 / 465)
#   - IMAP and IMAP-SSL for mail clients
#   - POP3 and POP3-SSL
#   - HTTPS for OWA / ActiveSync / Autodiscover (Exchange)
#   - Outbound SMTP relay to smart-host or internet
#   - Directory lookups (LDAP to group:dc for auth)
#
# Tag mail servers with  #tag:mail  in [TARGETS].
# ============================================================

[META]
NAME        = email_server
DESCRIPTION = Enterprise Email Server (Exchange / Postfix)
VERSION     = 1.0
TAGS        = email, exchange, postfix, smtp

[RULES]
# PROTO   ROLE     SRC   DST           PORT   INTV  CNT  #name

# --- Inbound SMTP ---
TCP       listen   SELF  -             25     -     -    #smtp-inbound
TCP       listen   SELF  -             587    -     -    #smtp-submission
TCP       listen   SELF  -             465    -     -    #smtps

# --- IMAP ---
TCP       listen   SELF  -             143    -     -    #imap
TCP       listen   SELF  -             993    -     -    #imaps

# --- POP3 ---
TCP       listen   SELF  -             110    -     -    #pop3
TCP       listen   SELF  -             995    -     -    #pop3s

# --- Exchange HTTPS (OWA / EAS / EWS / Autodiscover) ---
TCP       listen   SELF  -             443    -     -    #exchange-https

# --- Outbound SMTP relay ---
TCP       connect  SELF  ANY           25     60    2    #smtp-relay-outbound

# --- LDAP lookups to domain controllers ---
TCP       connect  SELF  group:dc      389    30    2    #ldap-auth
TCP       connect  SELF  group:dc      636    30    1    #ldaps-auth

# --- NTP ---
UDP       connect  SELF  ANY           123    60    1    #ntp-sync
