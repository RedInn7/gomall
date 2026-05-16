# 迁移说明

## v0 → v1：金额字段类型从 float64 改为 int64（分）

`order.money` 列由 `DOUBLE` 改为 `BIGINT`，单位从"元"变为"分"。
`user.money`（加密 string 字段）类型不变，但解密后的语义从"元"变为"分"。

### 影响

- 应用层 `Order.Money` 类型 `float64 → int64`
- `User.DecryptMoney` 返回值 `float64 → int64`
- `consts.UserInitMoney` 由 `"10000"`（元）改为 `"1000000"`（分）
- `types.PaymentDownReq.Money` 已被移除，金额一律服务端从订单计算

### 全新环境

GORM `AutoMigrate` 会在首次建表时直接用新类型，无需额外操作。

### 已有数据库

按下列顺序处理（建议先备份）：

1. **暂停写入流量**（停服或开维护页）

2. **校验现有数据**

   ```sql
   SELECT id, money FROM `order` WHERE money <> ROUND(money * 100) / 100;
   ```

   如果有非两位小数的脏数据，先决定取整策略。

3. **改 `order.money` 列类型**

   ```sql
   ALTER TABLE `order`
     ADD COLUMN money_cents BIGINT NOT NULL DEFAULT 0 AFTER money;

   UPDATE `order` SET money_cents = ROUND(money * 100);

   ALTER TABLE `order`
     DROP COLUMN money,
     CHANGE COLUMN money_cents money BIGINT NOT NULL DEFAULT 0;
   ```

4. **重新加密 `user.money`**

   该字段是 AES 加密的字符串。需要写一次性脚本：解密 → 数值 × 100 → 重新加密落库。
   脚本必须知道每个用户的 `key`，因此通常在用户下次登录/支付时 lazy 迁移：

   ```text
   if 旧格式（明文是 "10000.00" 风格的元）:
       cents = round(float(decrypted) * 100)
       store(encrypt(str(cents)))
   ```

5. **恢复流量**，观察支付/余额展示是否正常

### 教学环境（推荐）

直接 drop & recreate：

```bash
docker compose down -v
docker compose up -d
```

让 `AutoMigrate` 用新结构重建表。
