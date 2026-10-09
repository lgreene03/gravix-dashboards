{{/*
Expand the name of the chart.
*/}}
{{- define "gravix.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a fully qualified app name.
*/}}
{{- define "gravix.fullname" -}}
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

{{/*
Render imagePullSecrets if configured.
Usage: {{ include "gravix.imagePullSecrets" . }}
*/}}
{{- define "gravix.imagePullSecrets" -}}
{{- if .Values.global.imagePullSecrets }}
imagePullSecrets:
  {{- toYaml .Values.global.imagePullSecrets | nindent 2 }}
{{- end }}
{{- end }}

{{/*
Build a full image reference: registry/repo:tag
Usage: {{ include "gravix.image" (dict "registry" .Values.global.imageRegistry "repository" .Values.ingestion.image.repository "tag" .Values.ingestion.image.tag) }}
*/}}
{{- define "gravix.image" -}}
{{- if .registry -}}
{{- printf "%s/%s:%s" .registry .repository .tag -}}
{{- else -}}
{{- printf "%s:%s" .repository .tag -}}
{{- end -}}
{{- end }}

{{/*
The tenant database every analytics job reads (F-079). Without it a job runs
single-tenant and reads a prefix nothing writes. Postgres when
gateway.dbDriver is postgres. Otherwise the gateway's SQLite file, on the
gateway's volume: the volume is ReadWriteOnce, so the job is scheduled onto the
gateway's node, where a second pod may mount it. SQLite with no gateway volume
has no file a job could reach, and the job runs single-tenant.
*/}}
{{- define "gravix.tenantDB.sqlite" -}}
{{- and (ne (.Values.gateway.dbDriver | default "sqlite") "postgres") .Values.gateway.persistence.enabled -}}
{{- end }}

{{- define "gravix.tenantDB.env" -}}
{{- if eq (.Values.gateway.dbDriver | default "sqlite") "postgres" }}
- name: DB_DRIVER
  value: "postgres"
- name: DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ .Release.Name }}-secrets
      key: database-url
{{- else if eq (include "gravix.tenantDB.sqlite" .) "true" }}
- name: DB_DRIVER
  value: "sqlite"
- name: TENANT_DB_PATH
  value: {{ .Values.gateway.tenantDBPath | default "/data/gravix.db" | quote }}
{{- end }}
{{- end }}

{{- define "gravix.tenantDB.volumeMounts" -}}
{{- if eq (include "gravix.tenantDB.sqlite" .) "true" }}
volumeMounts:
  - name: tenant-db
    mountPath: /data
{{- end }}
{{- end }}

{{- define "gravix.tenantDB.podSpec" -}}
{{- if eq (include "gravix.tenantDB.sqlite" .) "true" }}
affinity:
  podAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      - labelSelector:
          matchLabels:
            app: gateway
        topologyKey: kubernetes.io/hostname
volumes:
  - name: tenant-db
    persistentVolumeClaim:
      claimName: {{ .Release.Name }}-gateway-pvc
{{- end }}
{{- end }}
