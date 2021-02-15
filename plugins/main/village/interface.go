package main

type IfCtrl interface {
	Add() error
	Del() error
	Check() error
}
