# AWS EC2 dev/test deploy — running docker-compose on a Linux VM

For when you're new to AWS and want to skip the full EKS migration. This
puts the entire docker-compose stack on a single Ubuntu VM so you can
share a working URL with testers + learn AWS basics without committing
to the ~$2000/month EKS bill.

**Cost: ~$150/month if running 24/7; ~$25/month if you stop the VM
between sessions.**

**What you get:**
- Real public URL (`http://<elastic-ip>` or your own domain)
- Everything from `docker compose up -d` running in the cloud
- Testers can hit it from anywhere
- You learn EC2, EBS, security groups, Elastic IP, SSH key
  management, AWS CLI

**What you trade:**
- Not production. Single point of failure, no SSL by default, no
  auto-scaling, no backups (you snapshot manually).
- The full EKS plan in `aws_staging_runbook.md` is your real
  production target. This is the on-ramp.

---

## 0. Before you start

### 0.1 Set a billing alert FIRST

This is the single most important step. Forgetting an EC2 instance
or accidentally launching the wrong size has burned every new AWS
user.

1. AWS Console → top-right account name → **Billing and Cost
   Management**
2. Left sidebar → **Budgets** → **Create budget**
3. Choose "Use a template" → "Monthly cost budget"
4. Name: `atpost-monthly-cap`
5. Budgeted amount: **$200** (covers the VM with headroom)
6. Email alerts when:
   - 50% actual ($100)
   - 80% actual ($160)
   - 100% forecasted ($200)
7. Save.

You'll get an email if something runs away. Don't skip this.

### 0.2 Install AWS CLI locally (Ubuntu)

```bash
sudo apt update
sudo apt install -y awscli unzip
aws configure
# AWS Access Key ID: <get from IAM → Users → Security credentials>
# AWS Secret Access Key: <same>
# Default region: ap-south-1   (Mumbai, lowest latency for India)
# Default output format: json
```

If you don't have a programmatic-access user yet:

1. AWS Console → IAM → Users → Create user
2. Name: `cli-admin`, attach `AdministratorAccess` policy (yes,
   broad — fine for solo learning; tighten later)
3. After create → "Security credentials" tab → Create access key →
   choose "Command Line Interface (CLI)" → copy + paste both
   values into the prompts above

### 0.3 Pick a region

`ap-south-1` (Mumbai) for India users. `us-east-1` is the cheapest
+ has the broadest service availability if you don't mind latency.
Stick with one — switching later means moving the VM.

---

## 1. Generate an SSH key pair

You need a key to SSH into the VM.

```bash
# Generate locally (Ubuntu)
mkdir -p ~/.ssh
ssh-keygen -t ed25519 -f ~/.ssh/atpost-ec2 -C "atpost-ec2"
# Press Enter twice for no passphrase, or set one if you prefer.
```

Upload the public key to AWS:

```bash
aws ec2 import-key-pair \
  --key-name atpost-ec2 \
  --public-key-material "fileb://$HOME/.ssh/atpost-ec2.pub"
```

Verify it landed:

```bash
aws ec2 describe-key-pairs --key-names atpost-ec2
```

---

## 2. Create a security group

A security group is an EC2 firewall. We need:

- **22 (SSH)** — only from YOUR IP (security)
- **80 (HTTP)** — from anywhere (so testers can hit it)
- **443 (HTTPS)** — from anywhere (once you add a TLS cert)
- **3000 (Next.js)** — from anywhere (for the postbook-ui dev server)
- **8080 (api-gateway)** — from anywhere (for the API entry point)

```bash
# Your current public IP — security group will pin SSH to this
MY_IP=$(curl -s ifconfig.me)/32
echo "Will allow SSH from $MY_IP"

# Create the SG in the default VPC
SG_ID=$(aws ec2 create-security-group \
  --group-name atpost-ec2 \
  --description "atpost docker-compose host" \
  --query 'GroupId' --output text)
echo "SG ID: $SG_ID"

# Rules
aws ec2 authorize-security-group-ingress --group-id $SG_ID \
  --protocol tcp --port 22 --cidr $MY_IP
aws ec2 authorize-security-group-ingress --group-id $SG_ID \
  --protocol tcp --port 80 --cidr 0.0.0.0/0
aws ec2 authorize-security-group-ingress --group-id $SG_ID \
  --protocol tcp --port 443 --cidr 0.0.0.0/0
aws ec2 authorize-security-group-ingress --group-id $SG_ID \
  --protocol tcp --port 3000 --cidr 0.0.0.0/0
aws ec2 authorize-security-group-ingress --group-id $SG_ID \
  --protocol tcp --port 8080 --cidr 0.0.0.0/0
```

If your public IP changes (most home ISPs rotate it), you'll need
to re-authorize SSH from the new IP. Alternative: open SSH from
`0.0.0.0/0` (less safe but simpler).

---

## 3. Launch the EC2 instance

### 3.1 Pick a size

**Recommendation: `m6i.xlarge`** — 4 vCPU, 16GB RAM, ~$140/month
running 24/7 in ap-south-1.

Reasoning: docker-compose runs ~30 Go services + Postgres + Scylla
+ Redis + Redpanda + OpenSearch + MinIO. Postgres alone uses ~2GB,
Scylla in developer mode ~2GB, OpenSearch ~2GB, Redpanda ~1GB,
~30 Go services × ~50MB = 1.5GB. Total ~10GB resident. 16GB is
comfortable; 8GB will swap on builds.

Smaller options if budget is tighter:
- `t3.large` (2 vCPU, 8GB) — ~$60/month. Will swap. Builds
  take 3-4× longer. OK if you only need a subset of services.
- `t3.xlarge` (4 vCPU, 16GB) — ~$120/month. Burstable CPU; fine
  if usage is bursty.

### 3.2 Get the latest Ubuntu 24.04 LTS AMI

```bash
AMI_ID=$(aws ec2 describe-images \
  --owners 099720109477 \
  --filters \
    "Name=name,Values=ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*" \
    "Name=state,Values=available" \
  --query 'sort_by(Images, &CreationDate)[-1].ImageId' \
  --output text)
echo "AMI: $AMI_ID"
```

### 3.3 Launch

```bash
INSTANCE_ID=$(aws ec2 run-instances \
  --image-id $AMI_ID \
  --instance-type m6i.xlarge \
  --key-name atpost-ec2 \
  --security-group-ids $SG_ID \
  --block-device-mappings 'DeviceName=/dev/sda1,Ebs={VolumeSize=100,VolumeType=gp3,DeleteOnTermination=true}' \
  --tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=atpost-dev}]' \
  --query 'Instances[0].InstanceId' --output text)
echo "Instance: $INSTANCE_ID"

# Wait for the instance to be running (30-60 seconds)
aws ec2 wait instance-running --instance-ids $INSTANCE_ID
```

---

## 4. Assign an Elastic IP

Without this, the VM's public IP changes every time you stop +
restart it — which means SSH commands, DNS records, and testers'
bookmarks break every reboot.

```bash
ALLOC_ID=$(aws ec2 allocate-address \
  --query 'AllocationId' --output text)
PUBLIC_IP=$(aws ec2 associate-address \
  --instance-id $INSTANCE_ID \
  --allocation-id $ALLOC_ID \
  --query 'PublicIp' --output text)
echo "Public IP: $PUBLIC_IP"
```

**Important cost note:** an Elastic IP costs $0 when attached to a
running instance, but **$3.60/month if the instance is stopped or
the IP is detached**. So if you plan to stop the VM between
sessions, that detached IP keeps billing. Releasing + re-allocating
on each session works but gives you a fresh IP each time.

---

## 5. SSH in

```bash
ssh -i ~/.ssh/atpost-ec2 ubuntu@$PUBLIC_IP
# First time: "Are you sure you want to continue connecting? yes"
```

If you hit "Permission denied (publickey)":
- Check key permissions: `chmod 600 ~/.ssh/atpost-ec2`
- Check SG allows your IP: re-run step 2 with current IP
- Check instance is actually running: `aws ec2 describe-instances --instance-ids $INSTANCE_ID --query 'Reservations[0].Instances[0].State'`

---

## 6. Install Docker on the VM

From inside the SSH session:

```bash
# Update + install Docker via the official convenience script
sudo apt update -y
curl -fsSL https://get.docker.com | sudo sh

# Allow non-root user to run docker
sudo usermod -aG docker $USER

# Pick up the new group membership without logging out
newgrp docker

# Verify
docker --version
docker compose version    # comes bundled with recent Docker
```

---

## 7. Clone the repo + bring the stack up

```bash
sudo apt install -y git jq
git clone -b feat/vchat-rebrand-realtime-ui \
  https://github.com/rvreddy476/modernsmapp.git atpost
cd atpost/Architecture/docker

# Copy the example env if your repo has one
ls -la *.env *.example 2>/dev/null

# First-run config — review + edit as needed
# (Local-dev defaults are fine; change only if you have specific
#  third-party keys for Razorpay/Setu/Shiprocket/LiveKit/FCM.)
nano local.env    # Ctrl-X to save

# Bring it all up
docker compose up -d
```

The first run downloads + builds every image. Expect **15-30 minutes**.
Watch progress:

```bash
docker compose logs -f --tail 100
```

Once it settles, check what's running:

```bash
docker compose ps
```

You should see all ~40 containers in `Up` state. Any `Restarting`
or `Exited`? `docker compose logs <service>` to investigate.

---

## 8. Verify from your laptop

```bash
# From your laptop, NOT the VM
curl http://$PUBLIC_IP:8080/livez
# {"status":"alive"}

# Or open in browser
echo "Open this in your browser: http://$PUBLIC_IP:3000"
```

If the API is reachable but the web UI isn't, the postbook-ui
container may be on a different port. Check:

```bash
ssh ubuntu@$PUBLIC_IP "docker compose -f atpost/Architecture/docker/docker-compose.yml ps | grep ui"
```

---

## 9. Optional — point a domain at it

The raw `http://1.2.3.4:8080` works for testing, but you probably
want `https://test.cleestudio.com` for sharing.

### 9.1 Cloudflare (if your domain is there)

1. Cloudflare dashboard → cleestudio.com → DNS
2. Add an A record:
   - Name: `test`
   - IPv4: `$PUBLIC_IP`
   - Proxy status: **DNS only** (grey cloud, NOT orange)
     — Cloudflare-proxied + non-standard ports has gotchas; start
       with DNS-only and switch later.
3. After 1-2 minutes: `http://test.cleestudio.com:8080/livez`
   should work.

For HTTPS, the cleanest path is putting nginx + Let's Encrypt on
the VM. That's a separate runbook; skip for now and use HTTP.

### 9.2 Route 53 (if you want everything in AWS)

```bash
# Get your hosted zone ID for cleestudio.com (if you have one in R53)
ZONE_ID=$(aws route53 list-hosted-zones --query 'HostedZones[?Name==`cleestudio.com.`].Id' --output text | sed 's|/hostedzone/||')

aws route53 change-resource-record-sets \
  --hosted-zone-id $ZONE_ID \
  --change-batch '{
    "Changes": [{
      "Action": "UPSERT",
      "ResourceRecordSet": {
        "Name": "test.cleestudio.com",
        "Type": "A",
        "TTL": 60,
        "ResourceRecords": [{"Value": "'$PUBLIC_IP'"}]
      }
    }]
  }'
```

---

## 10. Save money — stop the VM when not in use

```bash
# Stop (preserves the EBS volume + data; no compute charge while stopped)
aws ec2 stop-instances --instance-ids $INSTANCE_ID

# Resume later
aws ec2 start-instances --instance-ids $INSTANCE_ID
# Instance comes back with the same Elastic IP attached.
```

Stopped instance is **$0 compute** but you still pay for:
- EBS volume: 100 GB gp3 ≈ $8/month
- Elastic IP detached state if you also release: $0; if attached
  to a stopped instance: ~$3.60/month

So a stopped instance costs ~$12/month idle. A running m6i.xlarge
is ~$140/month.

**Tip:** set a CloudWatch scheduled event to auto-stop the
instance every night at 11pm. Reduces accidental cost.

---

## 11. Destroy everything (when you're done learning)

```bash
# Terminate the instance (deletes the VM + the attached EBS volume
# if DeleteOnTermination was true, which we set in step 3.3)
aws ec2 terminate-instances --instance-ids $INSTANCE_ID

# Release the Elastic IP (stops the per-month charge)
aws ec2 release-address --allocation-id $ALLOC_ID

# Delete the security group (no charge, but cleanup)
aws ec2 delete-security-group --group-id $SG_ID

# Delete the key pair (no charge, but cleanup)
aws ec2 delete-key-pair --key-name atpost-ec2
```

After this, your AWS bill for atpost goes to $0.

---

## Troubleshooting

### "Connection timed out" on SSH
- Security group blocking your IP. Re-run step 2 with `curl -s ifconfig.me`.
- Instance not yet booted; wait 60s after launch.
- Wrong key. Check `~/.ssh/atpost-ec2` exists + `chmod 600`.

### `docker compose up` runs out of memory / OOM
- Too small an instance. Bump to `m6i.2xlarge` (32GB) for breathing
  room: `aws ec2 modify-instance-attribute --instance-id $INSTANCE_ID --instance-type m6i.2xlarge` (after stopping the instance).
- Or run a subset of services: edit docker-compose.yml or use
  `docker compose up -d post-service feed-service media-service`
  with just what you need to test.

### EBS volume out of disk
- 100GB fills up fast with images + logs + Scylla data. `df -h` to
  check. Resize the volume:
  ```bash
  aws ec2 modify-volume --volume-id <vol-id> --size 200
  # Then on the VM: sudo growpart /dev/sda 1 && sudo resize2fs /dev/sda1
  ```

### Web UI loads but API calls fail
- The frontend probably points at `localhost:8080`. Set
  `NEXT_PUBLIC_API_BASE_URL=http://<public-ip>:8080` in the
  postbook-ui container env + restart it.

### Costs higher than expected
- Open Billing → Cost Explorer → "Group by: Service". Look for:
  - **Data Transfer**: large egress (esp. to YouTube-like media
    serving) bills $0.09/GB after the first 100GB.
  - **NAT Gateway**: if you ended up using a private subnet, NAT
    is ~$32/month. Default VPC has none — you're probably fine.
  - **CloudWatch Logs**: high-volume slog can rack up. Set retention.

---

## When to graduate to EKS

You're ready to move to the full EKS plan when:

1. The docker-compose stack on this VM no longer feels constrained
   (you've hit memory ceilings, CPU bottlenecks, or want HA).
2. You need TLS + a real CDN for media.
3. Multiple testers complain about reliability ("it was down at 3am").
4. You're considering production launch.

The migration path: see `docs/deploy/aws_staging_runbook.md`. The
in-cluster everything (Aurora, MSK, OpenSearch, ScyllaDB Operator)
is much more capable but takes ~$2000/month + 4-6 hours of setup.

---

## Summary checklist

```
[ ] Billing alert set ($200/month)
[ ] AWS CLI installed + configured
[ ] SSH key generated + uploaded
[ ] Security group created
[ ] EC2 m6i.xlarge launched
[ ] Elastic IP attached
[ ] SSH'd in successfully
[ ] Docker + Docker Compose installed
[ ] Repo cloned
[ ] docker compose up -d completes
[ ] /livez returns 200 from the public IP
[ ] Optional: domain points at the public IP
[ ] Stop instance when done to save cost
```

Total time to first running test environment: **~90 minutes** if
nothing surprises you, half a day if you hit one of the
troubleshooting items above.
