CREATE TABLE credit_transactions (
  id           VARCHAR(36)   NOT NULL COMMENT 'ULID',
  user_id      VARCHAR(36)   NOT NULL COMMENT '用户 ID',
  type         ENUM('topup','usage','refund','adjustment') NOT NULL,
  service_name VARCHAR(64)   NULL COMMENT '来源业务',
  amount       BIGINT        NOT NULL COMMENT '积分变化量，正=增加，负=减少',
  balance_after BIGINT       NOT NULL COMMENT '交易后余额快照',
  description  VARCHAR(255)  NOT NULL DEFAULT '',
  status       ENUM('completed','cancelled') NOT NULL DEFAULT 'completed',
  auth_id      VARCHAR(36)   NULL COMMENT '关联 pre_auths.id',
  request_id   VARCHAR(128)  NULL COMMENT '全链路追踪 ID',
  created_at   DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  PRIMARY KEY (id),
  KEY idx_ct_user_created      (user_id, created_at),
  KEY idx_ct_user_type_created (user_id, type, created_at),
  KEY idx_ct_service           (service_name, created_at),
  KEY idx_ct_auth_id           (auth_id),
  KEY idx_ct_request_id        (request_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='积分流水，InsertOnly';
