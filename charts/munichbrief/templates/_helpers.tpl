{{- define "munichbrief.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "munichbrief.publicPaths" -}}
- path: /
  pathType: Exact
  backend:
    service:
      name: {{ include "munichbrief.fullname" . }}
      port:
        name: http
- path: /about
  pathType: Exact
  backend:
    service:
      name: {{ include "munichbrief.fullname" . }}
      port:
        name: http
- path: /robots.txt
  pathType: Exact
  backend:
    service:
      name: {{ include "munichbrief.fullname" . }}
      port:
        name: http
- path: /sitemap.xml
  pathType: Exact
  backend:
    service:
      name: {{ include "munichbrief.fullname" . }}
      port:
        name: http
- path: /de
  pathType: Prefix
  backend:
    service:
      name: {{ include "munichbrief.fullname" . }}
      port:
        name: http
- path: /en
  pathType: Prefix
  backend:
    service:
      name: {{ include "munichbrief.fullname" . }}
      port:
        name: http
- path: /incidents
  pathType: Prefix
  backend:
    service:
      name: {{ include "munichbrief.fullname" . }}
      port:
        name: http
- path: /static
  pathType: Prefix
  backend:
    service:
      name: {{ include "munichbrief.fullname" . }}
      port:
        name: http
- path: /social
  pathType: Prefix
  backend:
    service:
      name: {{ include "munichbrief.fullname" . }}
      port:
        name: http
- path: /healthz
  pathType: Exact
  backend:
    service:
      name: {{ include "munichbrief.fullname" . }}
      port:
        name: http
- path: /readyz
  pathType: Exact
  backend:
    service:
      name: {{ include "munichbrief.fullname" . }}
      port:
        name: http
{{- end }}

{{- define "munichbrief.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "munichbrief.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "munichbrief.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}

{{- define "munichbrief.selectorLabels" -}}
app.kubernetes.io/name: {{ include "munichbrief.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "munichbrief.image" -}}
{{- if .Values.image.digest -}}
{{ printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else -}}
{{ printf "%s:%s" .Values.image.repository .Values.image.tag }}
{{- end -}}
{{- end }}

{{- define "munichbrief.adminMiddleware" -}}
{{- if .Values.admin.basicAuthMiddleware -}}
{{- .Values.admin.basicAuthMiddleware -}}
{{- else -}}
{{- printf "%s-%s-admin-auth@kubernetescrd" .Release.Namespace (include "munichbrief.fullname" .) -}}
{{- end -}}
{{- end }}
