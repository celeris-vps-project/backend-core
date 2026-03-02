# backend-core

## Tests

Run the application-layer auth tests:

```powershell
go test ./internal/indentity/application
```

## Billing

Run the billing invoice tests:

```powershell
go test ./internal/billing/...
```


# agent
````
AGENT_VIRT_BACKEND=libvirt|incus|stub
AGENT_LIBVIRT_URI=qemu:///system        # libvirt only
AGENT_INCUS_PROJECT=default             # incus only
```