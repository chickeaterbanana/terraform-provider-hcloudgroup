#cloud-config
# Slot {{ .SlotID }} of group {{ .GroupName }}, generation {{ .Generation }}.
# Rendered at {{ .Now }} on the runner before Server.Create.

packages:
  - curl
  - unzip

write_files:
  - path: /etc/consul.d/peers.json
    permissions: "0644"
    owner: root:root
    content: |
      {{ "{" }}"retry_join": [
        {{- range $i, $p := .Peers }}{{ if $i }},{{ end }}
        "{{ $p.PrivateIP }}"
        {{- end }}
      ]{{ "}" }}

  - path: /etc/consul.d/server.json
    permissions: "0644"
    owner: root:root
    content: |
      {{ "{" }}
        "server": true,
        "node_name": "{{ .ServerName }}",
        {{- if eq (len .Peers) 0 }}
        "bootstrap_expect": 3,
        {{- end }}
        "datacenter": "default"
      {{ "}" }}

runcmd:
  - systemctl enable consul
  - systemctl start consul
