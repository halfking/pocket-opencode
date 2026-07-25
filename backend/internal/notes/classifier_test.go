package notes

import (
	"testing"
)

func TestClassifier_Classify_Technical(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("用Go实现了一个并发安全的缓存，使用sync.RWMutex保证线程安全")
	if result.Type != "tech" {
		t.Errorf("expected tech, got %s", result.Type)
	}
}

func TestClassifier_Classify_Meeting(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("今天下午3点开周会，讨论Sprint 30的进度")
	if result.Type != "meeting" {
		t.Errorf("expected meeting, got %s", result.Type)
	}
}

func TestClassifier_Classify_Todo(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("记得明天给张三发邮件确认API接口文档")
	if result.Type != "todo" {
		t.Errorf("expected todo, got %s", result.Type)
	}
}

func TestClassifier_Classify_Idea(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("突然想到一个主意，可以把AI和笔记结合起来")
	if result.Type != "idea" {
		t.Errorf("expected idea, got %s", result.Type)
	}
}

func TestClassifier_Classify_Product(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("用户反馈需要增加一个导出PDF的功能")
	if result.Type != "product" {
		t.Errorf("expected product, got %s", result.Type)
	}
}

func TestClassifier_Classify_Learning(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("学习Kubernetes的Pod调度原理，笔记整理如下")
	if result.Type != "learning" {
		t.Errorf("expected learning, got %s", result.Type)
	}
}

func TestClassifier_Classify_Default(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("今天天气不错")
	if result.Type != "general" {
		t.Errorf("expected general, got %s", result.Type)
	}
}

func TestClassifier_ExtractTags(t *testing.T) {
	c := &Classifier{}
	tags := c.ExtractTags("用Go语言实现了一个Docker容器管理工具")
	if len(tags) == 0 {
		t.Error("expected at least one tag")
	}
}