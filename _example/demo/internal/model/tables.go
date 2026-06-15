package model

import (
	"github.com/crazy-airhead/aifei-go/_example/demo/internal/model/user"

	aifeigen "github.com/crazy-airhead/aifei-go/generator"
)

var Tables = []*aifeigen.Table{

	user.Table,
}
