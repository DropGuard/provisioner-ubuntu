#!/bin/bash
curl -sSL "https://api.github.com/repos/clash-verge-rev/clash-verge-rev/releases/latest" -o response.json
jq -r '.assets[] | select(.name | test(".*_amd64\\.deb$")) | .browser_download_url' response.json
