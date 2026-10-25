#!/usr/bin/env bash
# Hardens the media bucket on AWS S3 (idempotent; MinIO lacks these APIs, dev doesn't need them).
#
# Usage: deploy/scripts/s3-media-bucket.sh <bucket> <app-origin>
#   e.g. deploy/scripts/s3-media-bucket.sh mindmesh-prod-media https://api.mindmesh.dev
#
# The media service only reads and writes objects; it should not hold bucket-admin rights.
set -euo pipefail

bucket="${1:?usage: s3-media-bucket.sh <bucket> <app-origin>}"
origin="${2:?usage: s3-media-bucket.sh <bucket> <app-origin>}"

# ACLs off: the bucket owner owns every object, whoever uploads it.
aws s3api put-bucket-ownership-controls --bucket "$bucket" \
  --ownership-controls 'Rules=[{ObjectOwnership=BucketOwnerEnforced}]'

# Objects are only reachable through presigned URLs, never publicly.
aws s3api put-public-access-block --bucket "$bucket" \
  --public-access-block-configuration \
  BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true

aws s3api delete-bucket-website --bucket "$bucket"

# Browsers PUT uploads straight to S3 with the headers the presigned URL signs; Content-Length
# is set by the browser and never preflighted.
aws s3api put-bucket-cors --bucket "$bucket" --cors-configuration "$(cat <<JSON
{
  "CORSRules": [{
    "AllowedOrigins": ["$origin"],
    "AllowedMethods": ["PUT", "GET", "HEAD"],
    "AllowedHeaders": ["content-type", "x-amz-checksum-sha256"],
    "ExposeHeaders": ["ETag"],
    "MaxAgeSeconds": 3600
  }]
}
JSON
)"

echo "s3-media-bucket: $bucket hardened (CORS origin $origin)"
