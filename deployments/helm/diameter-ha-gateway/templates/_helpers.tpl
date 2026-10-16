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
