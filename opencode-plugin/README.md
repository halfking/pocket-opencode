# OpenCode Pocket Plugin

OpenCode plugin for integrating with Pocket Backend, enabling real-time synchronization and remote control.

## Features

- 🔄 **Auto-Registration**: Automatically register OpenCode instance with Pocket Backend
- 📡 **Real-time Sync**: WebSocket-based bidirectional communication
- 👀 **Session Monitoring**: Monitor and report session creation, updates, and completion
- 🎮 **Remote Control**: Receive and execute remote commands
- 💓 **Health Reporting**: Regular heartbeat and status updates
- 🔌 **Auto-Reconnect**: Automatic reconnection on connection loss

## Installation

```bash
npm install @opencode-pocket/plugin
```

## Configuration

Create `.opencode/pocket-plugin.json`:

```json
{
  "backendURL": "wss://pocket.your-domain.com",
  "instanceID": "dev-macbook-pro",
  "displayName": "开发 MacBook Pro",
  "autoRegister": true,
  "reportInterval": 30,
  "auth": {
    "type": "token",
    "token": "your-instance-token"
  }
}
```

## Usage

### As OpenCode Plugin

```typescript
// In your OpenCode plugin entry
import OpenCodePocketPlugin from '@opencode-pocket/plugin'

const config = {
  backendURL: 'wss://pocket.your-domain.com',
  instanceID: 'my-instance',
  displayName: 'My OpenCode Instance',
  autoRegister: true,
  reportInterval: 30,
  auth: {
    type: 'token',
    token: process.env.POCKET_TOKEN
  }
}

const plugin = new OpenCodePocketPlugin(config)

// Activate plugin
await plugin.activate()

// Listen to events
plugin.on('connected', () => {
  console.log('Connected to Pocket Backend')
})

plugin.on('command', (command) => {
  console.log('Received command:', command)
})

// Deactivate when done
await plugin.deactivate()
```

### Standalone Usage

```typescript
import OpenCodePocketPlugin from '@opencode-pocket/plugin'
import config from './.opencode/pocket-plugin.json'

const plugin = new OpenCodePocketPlugin(config)

await plugin.activate()

// Plugin will now:
// 1. Connect to Backend via WebSocket
// 2. Register the instance
// 3. Monitor sessions
// 4. Send regular heartbeats
// 5. Handle remote commands
```

## API

### `OpenCodePocketPlugin`

#### Methods

- `activate()`: Activate the plugin
- `deactivate()`: Deactivate the plugin
- `on(event, handler)`: Listen to events

#### Events

- `connected`: WebSocket connected
- `disconnected`: WebSocket disconnected
- `registered`: Instance registered
- `command`: Remote command received
- `error`: Error occurred

## Architecture

```
OpenCode Instance
    ↓
OpenCode Pocket Plugin (this package)
    ↓ WebSocket
Pocket Backend
    ↓ WebSocket
Mobile App
```

## Message Types

### Outgoing (Plugin → Backend)

- `instance.register`: Register instance
- `instance.unregister`: Unregister instance
- `session.created`: Session created
- `session.updated`: Session updated
- `session.completed`: Session completed
- `heartbeat`: Heartbeat ping
- `command.result`: Command execution result

### Incoming (Backend → Plugin)

- `command`: Remote control command
- `ping`: Ping request

## Development

```bash
# Install dependencies
npm install

# Build
npm run build

# Watch mode
npm run dev

# Type check
npm run typecheck

# Lint
npm run lint

# Test
npm run test
```

## License

MIT
