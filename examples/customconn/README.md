# Custom Connection Example

This example demonstrates how to use third-party managed network connections with the zeroconf resolver.

## Features

- **Custom Connection Management**: Create and manage your own IPv4/IPv6 multicast connections
- **External Lifecycle Control**: You have full control over when connections are created and closed
- **Connection Reuse**: Share connections across multiple resolvers or other network operations
- **Flexible Configuration**: Configure connections with custom settings before passing them to the resolver

## Why Use Custom Connections?

1. **Connection Sharing**: Share a single connection across multiple resolvers or other mDNS operations
2. **Custom Configuration**: Apply custom socket options, buffer sizes, or other low-level settings
3. **Lifecycle Control**: Manage connection lifecycle independently of the resolver
4. **Connection Pooling**: Reuse connections in long-running applications

## Usage

```bash
# Run with default settings
go run client.go

# Customize service and timeout
go run client.go -service="_http._tcp" -domain="local" -wait=30
```

## Key Points

1. **Connection Creation**: The example shows how to create IPv4 and IPv6 multicast connections manually
2. **Resolver Integration**: Connections are passed to the resolver via `WithCustomConn` option
3. **Lifecycle Management**: Connections are closed by the application, not by the resolver
4. **Error Handling**: The example handles cases where one connection type fails to create

## Important Notes

- When using custom connections, **you are responsible** for closing them
- The resolver will **not close** custom connections when it shuts down
- Make sure to properly configure multicast group joins for your connections
- Custom connections must be properly configured for mDNS (port 5353, multicast groups, etc.)
- If you need to disconnect/reconnect connections, you may need to recreate the resolver
- Connections can be shared across multiple resolvers if needed

## Example Output

```
=== Custom Connection Example ===
This example demonstrates how to use third-party managed connections with zeroconf resolver.

Found 2 multicast interface(s)
Creating custom IPv4 connection...
✓ IPv4 connection created successfully
Creating custom IPv6 connection...
✓ IPv6 connection created successfully

Creating resolver with custom connections...
✓ Resolver created successfully

Starting service discovery for '_workstation._tcp' in domain 'local' (timeout: 20s)...

=== Service Discovery Results ===

[1] Service Found:
  Instance: MyComputer
  Service: _workstation._tcp
  Domain: local
  Host: MyComputer.local.
  Port: 9
  IPv4: [192.168.1.100]
  ...

=== Discovery Complete (Total: 5 services) ===

Closing custom connections...
✓ IPv4 connection closed
✓ IPv6 connection closed

=== Example Complete ===
Note: The custom connections were closed by us, not by the resolver.
```

