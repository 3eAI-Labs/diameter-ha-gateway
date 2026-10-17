{{/* vim: set filetype=helm: */}}

{{/* Expand the name of the chart. */}}
{{- define "diameter-ha-gateway.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Fully qualified app name (max 63 chars, DNS spec). */}}
{{- define "diameter-ha-gateway.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* Chart name and version as a label value. */}}
{{- define "diameter-ha-gateway.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Common labels. */}}
{{- define "diameter-ha-gateway.labels" -}}
helm.sh/chart: {{ include "diameter-ha-gateway.chart" . }}
{{ include "diameter-ha-gateway.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: telco
{{- end -}}

{{/* Selector labels. */}}
{{- define "diameter-ha-gateway.selectorLabels" -}}
app.kubernetes.io/name: {{ include "diameter-ha-gateway.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* ServiceAccount name. */}}
{{- define "diameter-ha-gateway.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "diameter-ha-gateway.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end -}}

{{/* Labels of the bundled NATS. Its own app.kubernetes.io/name, so the gateway's Service,
Deployment, PDB and NetworkPolicy selectors never match NATS pods. */}}
{{- define "diameter-ha-gateway.natsSelectorLabels" -}}
app.kubernetes.io/name: {{ include "diameter-ha-gateway.name" . }}-nats
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "diameter-ha-gateway.natsLabels" -}}
helm.sh/chart: {{ include "diameter-ha-gateway.chart" . }}
{{ include "diameter-ha-gateway.natsSelectorLabels" . }}
app.kubernetes.io/component: nats
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: telco
{{- end -}}

{{/* True when the chart deploys its own NATS: no URL given and nats.deploy on. */}}
{{- define "diameter-ha-gateway.natsBundled" -}}
{{- if and (not .Values.config.nats.url) .Values.nats.deploy -}}true{{- end -}}
{{- end -}}

{{/* The NATS URL the gateway is configured with: config.nats.url when set, else the
bundled NATS, else a render error. */}}
{{- define "diameter-ha-gateway.natsURL" -}}
{{- if .Values.config.nats.url -}}
{{- .Values.config.nats.url -}}
{{- else if .Values.nats.deploy -}}
{{- printf "nats://%s-nats:4222" (include "diameter-ha-gateway.fullname" .) -}}
{{- else -}}
{{- fail "config.nats.url is required when nats.deploy=false: set it to the NATS your applications use." -}}
{{- end -}}
{{- end -}}
