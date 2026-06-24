package server

import (
	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/db"
)

// TxInterceptor returns an Interceptor that wraps the method in a database transaction.
// If the method returns an Output with code != 0, the transaction is rolled back.
func TxInterceptor() aifei.Interceptor {
	return aifei.InterceptorFunc(func(method string, in aifei.Input, invoke func() aifei.Output) aifei.Output {
		var out aifei.Output
		err := db.Transaction(func() error {
			out = invoke()
			if out != nil && out.Code() != 0 {
				return &rollbackError{}
			}
			return nil
		})
		if err != nil {
			if _, ok := err.(*rollbackError); ok {
				return out
			}
			return Fail("transaction error: " + err.Error())
		}
		return out
	})
}

type rollbackError struct{}

func (e *rollbackError) Error() string { return "rollback" }
