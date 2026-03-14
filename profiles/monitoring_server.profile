# ============================================================
# Traffic Profile: Enterprise Monitoring Server
#
# Covers a central monitoring / observability node
# (Prometheus, Zabbix, Nagios, Grafana, ELK stack, etc.):
#   - Receives syslog and metric data from all hosts
#   - Polls SNMP on network devices (ANY)
#   - Scrapes Prometheus exporters on app and DB nodes
#   - Hosts web dashboards (Grafana / Kibana)
#   - Sends alerts via SMTP outbound
#
# Tag monitoring nodes with  #tag:monitoring  in [TARGETS].
# ============================================================

[META]
NAME        = monitoring_server
DESCRIPTION = Enterprise Monitoring / Observability Server
VERSION     = 1.0
TAGS        = monitoring, prometheus, grafana, zabbix, syslog

[RULES]
# PROTO   ROLE     SRC   DST   PORT   INTV  CNT  #name

# --- Syslog ingest (UDP + TCP TLS) ---
UDP       listen   SELF  -     514    -     -    #syslog-udp
TCP       listen   SELF  -     514    -     -    #syslog-tcp
TCP       listen   SELF  -     6514   -     -    #syslog-tls

# --- Prometheus scrape endpoint (pull model) ---
TCP       listen   SELF  -     9090   -     -    #prometheus

# --- Grafana dashboard ---
TCP       listen   SELF  -     3000   -     -    #grafana

# --- Kibana / Elasticsearch ---
TCP       listen   SELF  -     5601   -     -    #kibana
TCP       listen   SELF  -     9200   -     -    #elasticsearch-http
TCP       listen   SELF  -     9300   -     -    #elasticsearch-cluster

# --- Logstash / Beats ingest ---
TCP       listen   SELF  -     5044   -     -    #logstash-beats
TCP       listen   SELF  -     5000   -     -    #logstash-tcp

# --- SNMP polling (outbound to all network devices) ---
UDP       connect  SELF  ANY   161    60    1    #snmp-poll

# --- Active checks / ping substitute (ICMP not used — use TCP probes) ---
TCP       connect  SELF  ANY   80     30    1    #http-healthcheck
TCP       connect  SELF  ANY   443    30    1    #https-healthcheck

# --- Alert notifications via SMTP ---
TCP       connect  SELF  ANY   25     120   1    #smtp-alerts

# --- NTP ---
UDP       connect  SELF  ANY   123    60    1    #ntp-sync
