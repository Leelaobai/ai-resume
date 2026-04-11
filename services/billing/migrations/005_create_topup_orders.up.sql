CREATE TABLE topup_orders (
  id                VARCHAR(36)   NOT NULL COMMENT 'ULID',
  user_id           VARCHAR(36)   NOT NULL,
  credits           BIGINT        NOT NULL COMMENT '购买的积分数量',
  amount_fen        BIGINT        NOT NULL COMMENT '记账口径金额（分）',
  pay_currency      VARCHAR(8)    NOT NULL DEFAULT 'CNY',
  pay_amount_fen    BIGINT        NOT NULL COMMENT '实际支付金额（最小单位）',
  payment_channel   ENUM('wechat','alipay','stripe') NOT NULL,
  payment_order_id  VARCHAR(128)  NULL COMMENT '第三方支付平台订单号',
  status            ENUM('pending','paid','failed','refunded') NOT NULL DEFAULT 'pending',
  paid_at           DATETIME(3)   NULL,
  transaction_id    VARCHAR(36)   NULL COMMENT '到账后关联的 credit_transactions.id',
  created_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  deleted_at        DATETIME(3)   NULL COMMENT 'GORM 软删除',

  PRIMARY KEY (id),
  KEY idx_to_user_status    (user_id, status),
  KEY idx_to_payment_order  (payment_order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='充值订单';
