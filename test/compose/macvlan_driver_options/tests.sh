# -*- bash -*-

podman network inspect --format='{{ range . }} {{ .Options.mode }} {{ end }}' macvlan_driver_options_default
like "$output" "bridge" "$testname : network mode is set"
