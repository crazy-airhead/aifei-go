package generator

import "strings"

// buildNames derives the identifier names a table's generated code uses.
//
// Two naming scopes exist:
//
//   - default (one table per package): the bare historical names — Table,
//     New, Dao, FindById, ... — so output is unchanged.
//   - qualified (multiple tables share one package): every package-level
//     identifier carries the struct name — TableUser, NewUser, UserDao,
//     UserFindById, ... — so tables no longer collide. The prefix is
//     mechanical by design: pluralized names ("FindUsersBy") need a
//     pluralizer, which produces warts like "FlowTaskIndexs".
//
// slug is the prefix-stripped table name; it only matters in qualified mode,
// where each table's files are named base_<slug>.go etc. instead of base.go.
func buildNames(info *TableInfo, qualified bool, slug string) map[string]string {
	s := info.StructName

	// pre renders a type/function-position identifier (bare, or
	// struct-name-prefixed): Table / UserTable, FindBy / UserFindBy.
	pre := func(bare string) string {
		if qualified {
			return s + bare
		}
		return bare
	}
	// ctor renders a constructor-position identifier (bare, or
	// New<Struct><Rest>): NewDao / NewUserDao.
	ctor := func(rest string) string {
		if qualified {
			return "New" + s + rest
		}
		return "New" + rest
	}

	initRowFn := "initRow"
	listSqlVar := "listSql"
	newBaseFn := "NewBase"
	if qualified {
		initRowFn = "init" + s + "Row"
		listSqlVar = ToCamelCase(s) + "ListSql"
		newBaseFn = "New" + info.BaseName
	}

	baseFile, modelFile, daoFile, serviceFile := "base.go", "model.go", "dao.go", "service.go"
	if qualified {
		slug = strings.ToLower(slug)
		baseFile = "base_" + slug + ".go"
		modelFile = "model_" + slug + ".go"
		daoFile = "dao_" + slug + ".go"
		serviceFile = "service_" + slug + ".go"
	}

	tableVar := "Table"
	if qualified {
		// "Table" leads (TableUser), matching the BaseUser convention — unlike
		// the function/type names below where the struct name leads.
		tableVar = "Table" + s
	}

	return map[string]string{
		// base.go
		"tableVar":     tableVar,
		"newBaseFn":    newBaseFn, // NewBase / NewBaseUser
		"newWithRowFn": ctor("WithRow"),
		"initRowFn":    initRowFn,
		"fromRowFn":    pre("FromRow"),
		"fromRowsFn":   pre("FromRows"),

		// model.go
		"newFn": ctor(""),

		// dao.go
		"daoType":       pre("Dao"),
		"newDaoFn":      ctor("Dao"),
		"pageType":      s + "Page",
		"findByIdFn":    pre("FindById"),
		"deleteByIdFn":  pre("DeleteById"),
		"findByIdsFn":   pre("FindByIds"),
		"deleteByIdsFn": pre("DeleteByIds"),
		"findByFn":      pre("FindBy"),
		"findFirstByFn": pre("FindFirstBy"),
		"deleteByFn":    pre("DeleteBy"),
		"countFn":       pre("Count"),
		"countByFn":     pre("CountBy"),

		// service.go
		"serviceType": pre("Service"),
		"prefixConst": pre("ServicePrefix"),
		"listSqlVar":  listSqlVar,

		// file names
		"baseFile":    baseFile,
		"modelFile":   modelFile,
		"daoFile":     daoFile,
		"serviceFile": serviceFile,
	}
}

// ensureNames fills TableInfo.Names when absent (e.g. a TableInfo constructed
// by hand and passed straight to a sub-generator). It assumes default scope.
func ensureNames(info *TableInfo) {
	if info.Names == nil {
		info.Names = buildNames(info, false, info.Name)
	}
}

// mergeNames copies TableInfo.Names into a template data map so every
// template receives the identifier names alongside its own keys.
func mergeNames(data map[string]interface{}, info *TableInfo) {
	for k, v := range info.Names {
		data[k] = v
	}
}
