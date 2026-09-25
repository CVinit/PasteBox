# 对象存储调研笔记

调研日期：2026-06-09

## 免费对象存储结论

- Cloudflare R2 是游客临时文件的核心免费额度：10GB-month 免费存储、100 万 Class A 写请求、1000 万 Class B 读请求，出站流量免费。
- Backblaze B2 有 10GB 免费存储，但免费出站按平均存储量约 3 倍计算，10GB 免费存储只对应约 30GB/月免费出站，不适合高下载场景。
- Oracle OCI 有 20GB Always Free Object Storage、50,000 Object Storage API requests/month 和 10TB/month outbound data transfer；但请求额度太小，不适合高频游客上传。
- Google Cloud Storage/Firebase 可用 5GB-month、5,000 Class A、50,000 Class B 和 100GB 出站的 Always Free，但仅适合补充。
- Tigris 有 5GB Standard、10,000 Class A、100,000 Class B 免费额度，且 egress 免费，可做小规模补充。
- IBM、IDrive e2、Supabase、Scaleway、阿里 OSS、腾讯 COS 等存在免费、试用或促销额度，但口径或周期不适合当长期主容量。

游客 5MB、15 分钟删除时，存储不是瓶颈，写请求是瓶颈。按 R2 单账号计算：

```text
1,000,000 uploads/month * 5MB * 15min / 43,200min/month = about 1.7GB average storage
```

所以 R2 免费层大约可支撑 80 万到 100 万次游客 5MB 上传/月。若每次下载只消耗 1 个 GET，请求上限为 1000 万次下载/月；若 HEAD + GET，两者合计约 500 万次下载/月。

## 付费对象存储结论

### 优先推荐

- Cloudflare R2：适合游客临时文件、公开下载、早期免费/付费用户文件。单价 $0.015/GB-month，出站免费，无 Standard 最短保存期；请求费要监控。
- Backblaze B2：适合付费用户持久文件、备份和中低出站场景。$6.95/TB/month，3 倍平均存储量免费出站，无最短保存期。
- Hetzner Object Storage：适合 EU 区域、预算敏感、1TB 级别以上负载。$5.99 或 EUR 4.99/月含约 1TB 存储和约 1TB 出站，超出流量约 EUR 1/TB，API 免费。
- Tigris：适合想要 S3 兼容、全球分布、零出站费的备选。Standard $0.02/GB-month，Class A $0.005/1000，Class B $0.0005/1000，egress 免费。

### 不适合游客临时文件但可用于长期文件

- Wasabi：$6.99/TB/month，出站/API 免费，但 Pay-as-you-go 默认 90 天最短保存期，15 分钟删除会按剩余天数继续计费。
- IDrive e2：$5/TB/month，最低按 1TB 收费，免费出站口径和 3 倍 active storage 政策需要按账号条款复核。适合低成本长期存储，不适合高 SLA 网关主存储。

### 大厂对象存储

- AWS S3、Google Cloud Storage、Azure Blob：稳定、生态强，但出站流量和请求费用复杂。对 PasteBox 这种公开文件下载业务，成本容易被 egress 拉高。
- Oracle OCI：存储约 $0.0255/GB-month，亮点是 Always Free/租户层面 10TB/月 outbound allowance 和较低 egress，但整体更适合已经跑在 OCI 上的服务。
- IBM Cloud Object Storage：企业侧可选，Lite/Free 口径有冲突；One-Rate 适合企业新工作负载，但不适合作为 PasteBox 早期默认低成本方案。

## 推荐架构

- 游客临时文件：R2 主用，15 分钟 TTL，5MB 强限制，按 IP/账号限频，下载尽量直链，不让应用网关代理文件流。
- 免费登录用户：R2 或 B2，按账号给小容量配额；超过配额时限速或提示升级。
- 付费用户：优先 B2 或 Hetzner 作为低成本持久存储；公开下载量高的付费用户继续优先 R2/Tigris。
- 中间网关：先做 provider 抽象、限流、配额和成本监控，不建议一开始做复杂多云碎片化调度。

## 主要来源

- Cloudflare R2 Pricing: https://developers.cloudflare.com/r2/pricing/
- Backblaze B2 Pricing: https://www.backblaze.com/cloud-storage/pricing
- Backblaze B2 API Pricing: https://www.backblaze.com/cloud-storage/transaction-pricing
- Oracle Always Free Resources: https://docs.public.oneportal.content.oci.oraclecloud.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm
- Google Cloud Storage pricing/free tier: https://cloud.google.com/storage/pricing
- Tigris Pricing: https://www.tigrisdata.com/pricing
- IDrive e2 Pricing: https://www.idrive.com/s3-storage-e2/pricing
- Scaleway Storage Pricing: https://www.scaleway.com/en/pricing/storage/
- Wasabi Pricing: https://wasabi.com/pricing
- Wasabi Pricing FAQ: https://wasabi.com/pricing/faq
- Hetzner Object Storage: https://www.hetzner.com/storage/object-storage/
- Hetzner Object Storage docs: https://docs.hetzner.com/storage/object-storage/overview/
- OVHcloud Object Storage pricing: https://www.ovhcloud.com/en/public-cloud/prices/
- DigitalOcean Spaces Pricing: https://docs.digitalocean.com/products/spaces/details/pricing/
- Vultr Object Storage: https://www.vultr.com/products/object-storage/
- Azure Blob Storage pricing: https://azure.microsoft.com/en-us/pricing/details/storage/blobs/
- Azure cost examples: https://learn.microsoft.com/en-us/azure/storage/blobs/blob-storage-estimate-costs
- AWS S3 Pricing: https://aws.amazon.com/s3/pricing/
- Alibaba OSS free quota: https://www.alibabacloud.com/help/en/oss/free-quota-for-new-users
- Tencent COS free tier: https://www.tencentcloud.com/document/product/436/6240
