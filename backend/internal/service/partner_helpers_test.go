package service

import "errors"

// asAppErr — тонкая обёртка над errors.As для тестов пакета: они проверяют
// код и статус отказа, а не тип-ассерцию как таковую.
func asAppErr(err error, target any) bool { return errors.As(err, target) }
