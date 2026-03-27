# systemd Deployment

## Install

```bash
# Copy binary
sudo cp yai /usr/local/bin/yai
sudo chmod +x /usr/local/bin/yai

# Create user
sudo useradd -r -s /usr/sbin/nologin yai

# Create config dir
sudo mkdir -p /etc/yai
sudo cp yai.yaml /etc/yai/yai.yaml
sudo cp deploy/env.example /etc/yai/env
sudo chown -R yai:yai /etc/yai
sudo chmod 600 /etc/yai/env /etc/yai/yai.yaml

# Install service
sudo cp deploy/yai.service /etc/systemd/system/yai.service
sudo systemctl daemon-reload
sudo systemctl enable yai
sudo systemctl start yai
```

## Operations

```bash
# Check status
sudo systemctl status yai
sudo journalctl -u yai -f

# Reload config (no downtime — sends SIGHUP)
sudo systemctl reload yai

# Restart
sudo systemctl restart yai

# Update binary
sudo systemctl stop yai
sudo cp yai-new /usr/local/bin/yai
sudo systemctl start yai
```

## Environment Variables

Edit `/etc/yai/env` with your API keys:

```
YAI_ANTHROPIC_KEY=sk-ant-api03-xxxxx
YAI_DEEPSEEK_KEY=sk-xxxxx
```

Then reference them in `yai.yaml`:

```yaml
providers:
  - name: anthropic
    auth:
      type: x-api-key
      key: ${YAI_ANTHROPIC_KEY}
```

After editing env, reload the service:

```bash
sudo systemctl restart yai   # env changes require restart, not just reload
```
