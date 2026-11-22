# internal/price

本目录提供模型价格查询和远端价格更新. price.go 管理查询与网络同步, presets.go 提供生成的内置价格表; 查询优先使用 op 中的价格记录.

价格单位保持每 1M token 的美元价, 与 relay 的成本换算一致. 高频查询保持读锁, 远端更新在写锁下补充/覆盖现有价格表, 保留未被远端覆盖的内置数据.

调整价格刷新或优先级时读取 internal/price/price.go, 核对数据库价格与内置/远端价格的关系. presets.go 是生成文件, 更新内置价格时通过 scripts/updatePrice.py 的生成流程处理.
