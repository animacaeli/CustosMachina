package crypto

// 跨模块读写的密文字段标识集中在此（双真相源会漂移——v0.12.2 复核 N4：
// ci 用字面量而 release 用同值常量）。模块内独有的仍在各模块就近定义。
const (
	// AADRegistryCredential registries.credential：ci 模块写入、release 模块读取。
	AADRegistryCredential = "registries.credential"
	// AADProjectCIToken projects.ci_token：projects 模块写入、ci 模块读取。
	AADProjectCIToken = "projects.ci_token"
)
