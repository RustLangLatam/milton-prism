package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"
)

// conduitArticleModels reproduces the real patterns from conduit/articles/models.py
// in gothinkster/flask-realworld-example-app: SurrogatePK mixin (no explicit id),
// bare Column() (not db.Column()), reference_col() for FK helpers, relationship().
const conduitArticleModels = `
from conduit.database import SurrogatePK, Model, reference_col
from conduit.extensions import db
import datetime

favoriter_assoc = db.Table('favoritor_assoc',
    db.Column('favoritor_id', db.Integer, db.ForeignKey('userprofile.id')),
    db.Column('article_id', db.Integer, db.ForeignKey('article.id')))

tag_assoc = db.Table('tag_assoc',
    db.Column('tag_id', db.Integer, db.ForeignKey('tags.id')),
    db.Column('article_id', db.Integer, db.ForeignKey('article.id')))


class Tags(Model):
    __tablename__ = 'tags'
    id = db.Column(db.Integer, primary_key=True)
    tagname = db.Column(db.String(100), unique=True)

    def __init__(self, tagname):
        self.tagname = tagname

    def __repr__(self):
        return self.tagname


class Article(SurrogatePK, Model):
    __tablename__ = 'article'
    slug = Column(db.Text, unique=True)
    title = Column(db.String(100), nullable=False)
    description = Column(db.Text, nullable=False)
    body = Column(db.Text)
    createdAt = Column(db.DateTime, nullable=False, default=datetime.datetime.utcnow)
    updatedAt = Column(db.DateTime, nullable=False, default=datetime.datetime.utcnow)
    author_id = reference_col('userprofile', nullable=False)
    author = relationship('UserProfile', backref=db.backref('articles'))
    favoriters = relationship('UserProfile', secondary=favoriter_assoc, backref=db.backref('favorites'))
    tagList = relationship('Tags', secondary=tag_assoc, backref=db.backref('articles'))
    comments = relationship('Comment', backref=db.backref('article'))


class Comment(SurrogatePK, Model):
    __tablename__ = 'comment'
    body = Column(db.Text)
    author_id = reference_col('userprofile', nullable=False)
    author = relationship('UserProfile', backref=db.backref('comments'))
    article_id = reference_col('article', nullable=False)
    createdAt = Column(db.DateTime, nullable=False, default=datetime.datetime.utcnow)
    updatedAt = Column(db.DateTime, nullable=False, default=datetime.datetime.utcnow)
`

// conduitArticleViews reproduces conduit/articles/views.py (route declarations only).
const conduitArticleViews = `
from flask import Blueprint

articles_blueprint = Blueprint('articles', __name__)

@articles_blueprint.route('/api/articles/', methods=['GET'])
def get_articles():
    pass

@articles_blueprint.route('/api/articles/', methods=['POST'])
def create_article():
    pass

@articles_blueprint.route('/api/articles/<slug>', methods=['GET'])
def get_article(slug):
    pass

@articles_blueprint.route('/api/articles/<slug>', methods=['PUT'])
def update_article(slug):
    pass

@articles_blueprint.route('/api/articles/<slug>', methods=['DELETE'])
def delete_article(slug):
    pass

@articles_blueprint.route('/api/articles/<slug>/favorite', methods=['POST'])
def favorite_article(slug):
    pass

@articles_blueprint.route('/api/articles/<slug>/favorite', methods=['DELETE'])
def unfavorite_article(slug):
    pass

@articles_blueprint.route('/api/articles/<slug>/comments', methods=['POST'])
def create_comment(slug):
    pass

@articles_blueprint.route('/api/articles/<slug>/comments', methods=['GET'])
def get_comments(slug):
    pass

@articles_blueprint.route('/api/articles/<slug>/comments/<int:id>', methods=['DELETE'])
def delete_comment(slug, id):
    pass
`

// conduitUserModels reproduces the real conduit/user/models.py:
// User with snake_case timestamps (create_time/update_time after AIP rename)
// and token: str = ” as an annotated assignment (NOT a db.Column).
const conduitUserModels = `
from conduit.database import SurrogatePK, Model
from conduit.extensions import db
import datetime

class User(SurrogatePK, Model):
    __tablename__ = 'users'
    username = Column(db.String(80), unique=True, nullable=False)
    email = Column(db.String(80), unique=True, nullable=False)
    password = Column(db.Binary(128), nullable=True)
    created_at = Column(db.DateTime, nullable=False, default=datetime.datetime.utcnow)
    updated_at = Column(db.DateTime, nullable=False, default=datetime.datetime.utcnow)
    bio = Column(db.String(300), nullable=True)
    image = Column(db.String(120), nullable=True)
    token: str = ''
`

// conduitProfileModels reproduces the real conduit/profile/models.py:
// UserProfile with user_id as reference_col FK (the pattern invisible to the old parser).
const conduitProfileModels = `
from conduit.database import SurrogatePK, Model, reference_col
from conduit.extensions import db

class UserProfile(SurrogatePK, Model):
    __tablename__ = 'userprofile'
    id = db.Column(db.Integer, primary_key=True)
    user_id = reference_col('users', nullable=False)
    user = relationship('User', backref=db.backref('profile', uselist=False))
    follows = relationship(
        'UserProfile',
        secondary='followers',
        primaryjoin='UserProfile.id == followers.c.follower_id',
        secondaryjoin='UserProfile.id == followers.c.followed_id',
        backref=db.backref('followed_by', lazy='dynamic'),
        lazy='dynamic',
    )
`

const conduitUserViews = `
from flask import Blueprint

users_blueprint = Blueprint('user', __name__)

@users_blueprint.route('/api/users/', methods=['POST'])
def registration():
    pass

@users_blueprint.route('/api/users/login', methods=['POST'])
def login():
    pass

@users_blueprint.route('/api/user/', methods=['GET'])
def get_user():
    pass

@users_blueprint.route('/api/user/', methods=['PUT'])
def update_user():
    pass
`

// writeWorkspaceFiles writes Python source fixtures into a temp workspace.
func writeWorkspaceFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for relPath, content := range files {
		full := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return dir
}

// conduitTableServiceMap maps real Conduit __tablename__ values to service names.
// Derived from the real repo: userprofile (no 's') → profile, users → user.
var conduitTableServiceMap = map[string]string{
	"userprofile": "profile",  // UserProfile.__tablename__ in conduit/profile/models.py
	"users":       "user",     // User.__tablename__ in conduit/user/models.py
	"article":     "articles", // Article.__tablename__
	"comment":     "articles", // Comment.__tablename__
	"tags":        "articles", // Tags.__tablename__ (with 's')
}

// TestFlaskSQLAlchemyDeriver_ArticleMessages is the primary D3 acceptance test.
// It verifies the Article message from the REAL Conduit repo patterns:
//   - reference_col('userprofile', ...) → author_identifier (uint64, cross-FK to profile)
//   - relationship() fields are NOT included as proto fields
//   - camelCase timestamps (createdAt/updatedAt) are preserved as-is (no snake_case renaming)
func TestFlaskSQLAlchemyDeriver_ArticleMessages(t *testing.T) {
	workspace := writeWorkspaceFiles(t, map[string]string{
		"conduit/articles/models.py": conduitArticleModels,
		"conduit/articles/views.py":  conduitArticleViews,
		"conduit/__init__.py":        "from flask import Flask",
	})

	cluster := workerdomain.Cluster{
		BlueprintGroup: "conduit.articles",
		Modules: []workerdomain.Module{
			"conduit.articles",
			"conduit.articles.models",
			"conduit.articles.views",
			"conduit.articles.serializers",
		},
	}

	d := NewFlaskSQLAlchemyDeriver()
	contract, err := d.Derive(context.Background(), cluster, workspace, conduitTableServiceMap)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	t.Logf("proto path: %s", contract.ProtoPath)
	t.Logf("messages: %d", len(contract.Messages))
	for _, m := range contract.Messages {
		t.Logf("  %s: %d fields  relationships: %v", m.Name, len(m.Fields), m.Relationships)
		for _, f := range m.Fields {
			t.Logf("    [%d] %s %s  %s", f.Number, f.Type, f.Name, f.Comment)
		}
	}

	// Must derive 3 messages: Tags, Article, Comment.
	if len(contract.Messages) != 3 {
		t.Errorf("expected 3 messages (Tags, Article, Comment), got %d", len(contract.Messages))
	}

	// Find the Article message.
	var article *workerdomain.ProtoMessage
	for i := range contract.Messages {
		if contract.Messages[i].Name == "Article" {
			article = &contract.Messages[i]
		}
	}
	if article == nil {
		t.Fatal("Article message not found in derived proto")
	}

	fieldsByName := make(map[string]workerdomain.ProtoField)
	for _, f := range article.Fields {
		fieldsByName[f.Name] = f
	}

	// AIP field 1: identifier (uint64).
	if f, ok := fieldsByName["identifier"]; !ok {
		t.Error("Article missing 'identifier' field")
	} else {
		if f.Type != "uint64" {
			t.Errorf("identifier type: got %q, want uint64", f.Type)
		}
		if f.Number != 1 {
			t.Errorf("identifier field number: got %d, want 1", f.Number)
		}
	}

	// AIP field 2: state.
	if f, ok := fieldsByName["state"]; !ok {
		t.Error("Article missing 'state' field")
	} else if f.Number != 2 {
		t.Errorf("state field number: got %d, want 2", f.Number)
	}

	// Domain string fields from bare Column() calls (real Conduit pattern).
	for _, want := range []string{"slug", "title", "description", "body"} {
		f, ok := fieldsByName[want]
		if !ok {
			t.Errorf("Article missing field %q", want)
			continue
		}
		if f.Type != "string" {
			t.Errorf("Article.%s type: got %q, want string", want, f.Type)
		}
	}

	// author_id → author_identifier: uint64, cross-FK to userprofile (service: profile).
	// The real Conduit uses reference_col('userprofile', ...) — no 's'.
	if f, ok := fieldsByName["author_identifier"]; !ok {
		t.Error("Article missing 'author_identifier' (expected from author_id reference_col)")
	} else {
		if f.Type != "uint64" {
			t.Errorf("author_identifier type: got %q, want uint64", f.Type)
		}
		if !f.IsCrossFK {
			t.Error("author_identifier should be marked as cross-service FK")
		}
		if f.RefTable != "userprofile" {
			t.Errorf("author_identifier RefTable: got %q, want userprofile (real Conduit, no 's')", f.RefTable)
		}
		if f.RefService != "profile" {
			t.Errorf("author_identifier RefService: got %q, want profile", f.RefService)
		}
		if !strings.Contains(f.Comment, "(service: profile)") {
			t.Errorf("author_identifier Comment should contain '(service: profile)', got: %q", f.Comment)
		}
	}

	// createdAt/updatedAt from real Conduit must be AIP-normalised to create_time/update_time.
	for _, want := range []string{"create_time", "update_time"} {
		f, ok := fieldsByName[want]
		if !ok {
			t.Errorf("Article missing AIP timestamp field %q (createdAt/updatedAt must be normalised)", want)
			continue
		}
		if f.Type != "google.protobuf.Timestamp" {
			t.Errorf("Article.%s type: got %q, want google.protobuf.Timestamp", want, f.Type)
		}
	}

	// AIP soft-delete always present.
	for _, want := range []string{"delete_time", "purge_time"} {
		if _, ok := fieldsByName[want]; !ok {
			t.Errorf("Article missing AIP soft-delete field %q", want)
		}
	}

	// relationship() fields must NOT appear in proto fields.
	for _, bad := range []string{"author", "favoriters", "tagList", "comments"} {
		if _, ok := fieldsByName[bad]; ok {
			t.Errorf("Article should NOT have field %q (relationship, not a column)", bad)
		}
	}

	// But relationships must be in Relationships slice (not silently dropped).
	relNames := make(map[string]bool)
	for _, r := range article.Relationships {
		relNames[r] = true
	}
	if !relNames["author → UserProfile"] {
		t.Errorf("Article.Relationships missing 'author → UserProfile', got: %v", article.Relationships)
	}
}

// TestFlaskSQLAlchemyDeriver_ProfileMessages verifies the profile service contract
// against the REAL conduit/profile/models.py: UserProfile with user_id as
// reference_col('users', ...) — the FK pattern that was invisible to the old regex parser.
func TestFlaskSQLAlchemyDeriver_ProfileMessages(t *testing.T) {
	workspace := writeWorkspaceFiles(t, map[string]string{
		"conduit/profile/models.py": conduitProfileModels,
	})
	cluster := workerdomain.Cluster{
		BlueprintGroup: "conduit.profile",
		Modules:        []workerdomain.Module{"conduit.profile.models"},
	}
	tableMap := map[string]string{"users": "user"}

	d := NewFlaskSQLAlchemyDeriver()
	contract, err := d.Derive(context.Background(), cluster, workspace, tableMap)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	t.Logf("messages: %d", len(contract.Messages))
	for _, m := range contract.Messages {
		t.Logf("  %s: %d fields  relationships: %v", m.Name, len(m.Fields), m.Relationships)
		for _, f := range m.Fields {
			t.Logf("    [%d] %s %s  %s", f.Number, f.Type, f.Name, f.Comment)
		}
	}

	if len(contract.Messages) != 1 {
		t.Errorf("expected 1 message (UserProfile), got %d", len(contract.Messages))
	}
	var profile *workerdomain.ProtoMessage
	for i := range contract.Messages {
		if contract.Messages[i].Name == "UserProfile" {
			profile = &contract.Messages[i]
		}
	}
	if profile == nil {
		t.Fatal("UserProfile message not found — profile derivation broken (was the root cause of Problema 2)")
	}

	fieldsByName := make(map[string]workerdomain.ProtoField)
	for _, f := range profile.Fields {
		fieldsByName[f.Name] = f
	}

	// user_id → user_identifier: uint64, cross-FK to users (service: user).
	f, ok := fieldsByName["user_identifier"]
	if !ok {
		t.Fatal("UserProfile missing 'user_identifier' (expected from user_id reference_col)")
	}
	if f.Type != "uint64" {
		t.Errorf("user_identifier type: got %q, want uint64", f.Type)
	}
	if !f.IsCrossFK {
		t.Error("user_identifier should be marked as cross-service FK")
	}
	if f.RefTable != "users" {
		t.Errorf("user_identifier RefTable: got %q, want users", f.RefTable)
	}
	if f.RefService != "user" {
		t.Errorf("user_identifier RefService: got %q, want user", f.RefService)
	}

	// AIP synthetic fields.
	for _, want := range []string{"identifier", "state", "delete_time", "purge_time"} {
		if _, ok := fieldsByName[want]; !ok {
			t.Errorf("UserProfile missing AIP field %q", want)
		}
	}

	// user and follows are relationships — must NOT appear as proto fields.
	for _, bad := range []string{"user", "follows"} {
		if _, ok := fieldsByName[bad]; ok {
			t.Errorf("UserProfile should NOT have field %q (relationship, not a column)", bad)
		}
	}

	// Relationships must be annotated in the proto content.
	if !strings.Contains(contract.ProtoContent, "// Relationships") {
		t.Error("proto content should contain relationship annotations for UserProfile")
	}
}

// TestFlaskSQLAlchemyDeriver_ArticleRoutes verifies that CRUD routes become
// standard RPCs and non-CRUD routes (favorite, comments sub-resource) are TODO.
func TestFlaskSQLAlchemyDeriver_ArticleRoutes(t *testing.T) {
	workspace := writeWorkspaceFiles(t, map[string]string{
		"conduit/articles/models.py": conduitArticleModels,
		"conduit/articles/views.py":  conduitArticleViews,
	})
	cluster := workerdomain.Cluster{
		BlueprintGroup: "conduit.articles",
		Modules: []workerdomain.Module{
			"conduit.articles.models",
			"conduit.articles.views",
		},
	}

	d := NewFlaskSQLAlchemyDeriver()
	contract, err := d.Derive(context.Background(), cluster, workspace, nil)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	rpcNames := make(map[string]bool)
	for _, rpc := range contract.RPCs {
		if !rpc.IsTODO {
			rpcNames[rpc.Name] = true
		}
	}
	for _, want := range []string{"ListArticle", "CreateArticle", "GetArticle", "UpdateArticle", "DeleteArticle"} {
		if !rpcNames[want] {
			t.Errorf("expected CRUD rpc %q, not found. rpcNames=%v", want, rpcNames)
		}
	}

	todoRoutes := make(map[string]bool)
	for _, rpc := range contract.RPCs {
		if rpc.IsTODO {
			todoRoutes[rpc.Path] = true
		}
	}
	for _, want := range []string{
		"/api/articles/<slug>/favorite",
		"/api/articles/<slug>/comments",
		"/api/articles/<slug>/comments/<int:id>",
	} {
		if !todoRoutes[want] {
			t.Errorf("expected non-CRUD route %q to be TODO", want)
		}
	}

	if !contract.HasTODORoutes {
		t.Error("HasTODORoutes should be true when non-CRUD routes exist")
	}

	if !strings.Contains(contract.ProtoContent, "// TODO: custom route:") {
		t.Error("proto content missing TODO comment for custom routes")
	}
}

// TestFlaskSQLAlchemyDeriver_ProtoAIPShape verifies the generated proto is
// structurally AIP-compliant: correct syntax, package, timestamps import.
func TestFlaskSQLAlchemyDeriver_ProtoAIPShape(t *testing.T) {
	workspace := writeWorkspaceFiles(t, map[string]string{
		"conduit/articles/models.py": conduitArticleModels,
		"conduit/articles/views.py":  conduitArticleViews,
	})
	cluster := workerdomain.Cluster{
		BlueprintGroup: "conduit.articles",
		Modules:        []workerdomain.Module{"conduit.articles.models", "conduit.articles.views"},
	}

	d := NewFlaskSQLAlchemyDeriver()
	contract, err := d.Derive(context.Background(), cluster, workspace, nil)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	proto := contract.ProtoContent
	t.Logf("\n--- derived proto ---\n%s\n--- end ---", proto)

	checks := []struct {
		substr string
		desc   string
	}{
		{`syntax = "proto3"`, "syntax declaration"},
		{`import "google/protobuf/timestamp.proto"`, "timestamp import"},
		{`package generated.articles.v1`, "package"},
		{`message Article {`, "Article message"},
		{`uint64 identifier = 1`, "AIP identifier field 1"},
		{`ArticleState state = 2`, "AIP state field 2"},
		// camelCase createdAt/updatedAt from real Conduit must be AIP-normalised.
		{`google.protobuf.Timestamp create_time`, "create_time (AIP normalised from createdAt)"},
		{`google.protobuf.Timestamp update_time`, "update_time (AIP normalised from updatedAt)"},
		{`google.protobuf.Timestamp delete_time`, "delete_time (soft-delete)"},
		{`google.protobuf.Timestamp purge_time`, "purge_time (hard-delete)"},
		{`enum ArticleState {`, "ArticleState enum"},
		{`ARTICLE_UNSPECIFIED = 0`, "unspecified enum value"},
		{`ARTICLE_ACTIVE = 1`, "active enum value"},
		{`service ArticleService {`, "ArticleService"},
		{`// Relationships`, "relationship annotations"},
	}
	for _, c := range checks {
		if !strings.Contains(proto, c.substr) {
			t.Errorf("proto missing %s: %q", c.desc, c.substr)
		}
	}
}

// TestFlaskSQLAlchemyDeriver_UserMessages verifies the user service contract
// against the REAL conduit/user/models.py.
func TestFlaskSQLAlchemyDeriver_UserMessages(t *testing.T) {
	workspace := writeWorkspaceFiles(t, map[string]string{
		"conduit/user/models.py": conduitUserModels,
		"conduit/user/views.py":  conduitUserViews,
	})
	cluster := workerdomain.Cluster{
		BlueprintGroup: "conduit.user",
		Modules:        []workerdomain.Module{"conduit.user.models", "conduit.user.views"},
	}

	d := NewFlaskSQLAlchemyDeriver()
	contract, err := d.Derive(context.Background(), cluster, workspace, nil)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	if len(contract.Messages) != 1 {
		t.Errorf("expected 1 message (User), got %d", len(contract.Messages))
	}
	if len(contract.Messages) > 0 && contract.Messages[0].Name != "User" {
		t.Errorf("expected message User, got %q", contract.Messages[0].Name)
	}

	fieldsByName := make(map[string]workerdomain.ProtoField)
	if len(contract.Messages) > 0 {
		for _, f := range contract.Messages[0].Fields {
			fieldsByName[f.Name] = f
		}
	}

	// Domain fields from real user/models.py.
	for _, want := range []string{"identifier", "state", "username", "email", "bio", "image"} {
		if _, ok := fieldsByName[want]; !ok {
			t.Errorf("User missing field %q", want)
		}
	}
	// snake_case timestamps get AIP-renamed (created_at → create_time, updated_at → update_time).
	for _, want := range []string{"create_time", "update_time"} {
		if _, ok := fieldsByName[want]; !ok {
			t.Errorf("User missing AIP timestamp field %q", want)
		}
	}
	// token: str = '' is an annotated assignment — must NOT appear as a proto field.
	if _, ok := fieldsByName["token"]; ok {
		t.Error("User should NOT have 'token' field (it is annotated assignment token: str = '', not a Column)")
	}
}

// incompleteModels is a model file that the deriver CAN recognise as a SQLAlchemy
// class (has Model base) but produces zero extractable messages: the only column
// is id (skipped) and the rest are relationship() calls (not scalar fields).
// This exercises the Incomplete flag path without simulating missing files.
const incompleteModels = `
from conduit.database import Model
from conduit.extensions import db

class IdOnlyModel(Model):
    __tablename__ = 'idonly'
    id = db.Column(db.Integer, primary_key=True)
    items = relationship('Item', backref='model')
`

// TestFlaskSQLAlchemyDeriver_IncompleteMarker verifies that the Incomplete flag
// is set when a models module exists but the deriver extracts zero proto messages.
func TestFlaskSQLAlchemyDeriver_IncompleteMarker(t *testing.T) {
	workspace := writeWorkspaceFiles(t, map[string]string{
		"svc/models.py": incompleteModels,
	})
	cluster := workerdomain.Cluster{
		BlueprintGroup: "svc",
		Modules:        []workerdomain.Module{"svc.models"},
	}

	d := NewFlaskSQLAlchemyDeriver()
	contract, err := d.Derive(context.Background(), cluster, workspace, nil)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	if !contract.Incomplete {
		t.Error("expected Incomplete=true when models module present but zero messages derived")
	}
	if contract.IncompleteReason == "" {
		t.Error("expected non-empty IncompleteReason")
	}
	t.Logf("IncompleteReason: %q", contract.IncompleteReason)

	// Zero messages expected.
	if len(contract.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(contract.Messages))
	}
}

// TestFlaskSQLAlchemyDeriver_IncompleteMarker_MissingFile verifies the flag fires
// when the cluster lists a models module but the file does not exist on disk.
func TestFlaskSQLAlchemyDeriver_IncompleteMarker_MissingFile(t *testing.T) {
	workspace := t.TempDir() // empty — no files written
	cluster := workerdomain.Cluster{
		BlueprintGroup: "svc",
		Modules:        []workerdomain.Module{"svc.models"}, // listed but absent
	}

	d := NewFlaskSQLAlchemyDeriver()
	contract, err := d.Derive(context.Background(), cluster, workspace, nil)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	if !contract.Incomplete {
		t.Error("expected Incomplete=true when models module listed but file missing")
	}
	if contract.IncompleteReason == "" {
		t.Error("expected non-empty IncompleteReason")
	}
	t.Logf("IncompleteReason: %q", contract.IncompleteReason)
}

// TestFlaskSQLAlchemyDeriver_ConduitServicesComplete verifies that after F1,
// all three real Conduit services derive with Incomplete=false.
// This is the regression gate: no service should be incorrectly flagged.
func TestFlaskSQLAlchemyDeriver_ConduitServicesComplete(t *testing.T) {
	cases := []struct {
		name      string
		modelFile string
		content   string
		tableMap  map[string]string
	}{
		{
			name:      "articles",
			modelFile: "conduit/articles/models.py",
			content:   conduitArticleModels,
			tableMap:  conduitTableServiceMap,
		},
		{
			name:      "profile",
			modelFile: "conduit/profile/models.py",
			content:   conduitProfileModels,
			tableMap:  map[string]string{"users": "user"},
		},
		{
			name:      "user",
			modelFile: "conduit/user/models.py",
			content:   conduitUserModels,
			tableMap:  nil,
		},
	}

	d := NewFlaskSQLAlchemyDeriver()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := writeWorkspaceFiles(t, map[string]string{
				tc.modelFile: tc.content,
			})
			pkg := "conduit." + tc.name
			cluster := workerdomain.Cluster{
				BlueprintGroup: pkg,
				Modules:        []workerdomain.Module{workerdomain.Module(pkg + ".models")},
			}

			contract, err := d.Derive(context.Background(), cluster, workspace, tc.tableMap)
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}

			if contract.Incomplete {
				t.Errorf("service %q must not be incomplete after F1 fix, reason: %q",
					tc.name, contract.IncompleteReason)
			}
			if len(contract.Messages) == 0 {
				t.Errorf("service %q: expected at least 1 message, got 0", tc.name)
			}
			t.Logf("service=%s messages=%d incomplete=%v",
				tc.name, len(contract.Messages), contract.Incomplete)
		})
	}
}

// TestCRUDClassification_Table exercises classifyCRUD with a variety of routes.
func TestCRUDClassification_Table(t *testing.T) {
	cases := []struct {
		path       string
		method     string
		wantName   string
		wantIsTODO bool
	}{
		{"/api/articles/", "GET", "ListArticle", false},
		{"/api/articles/", "POST", "CreateArticle", false},
		{"/api/articles/<slug>", "GET", "GetArticle", false},
		{"/api/articles/<slug>", "PUT", "UpdateArticle", false},
		{"/api/articles/<slug>", "DELETE", "DeleteArticle", false},
		{"/api/articles/<slug>/favorite", "POST", "", true},
		{"/api/articles/<slug>/favorite", "DELETE", "", true},
		{"/api/articles/<slug>/comments", "GET", "", true},
		{"/api/articles/<slug>/comments/<int:id>", "DELETE", "", true},
		{"/api/users/login", "POST", "", true},
		{"/api/profiles/<username>/follow", "POST", "", true},
	}
	for _, tc := range cases {
		name, isTODO := classifyCRUD(tc.path, tc.method, "articles")
		if isTODO != tc.wantIsTODO {
			t.Errorf("classifyCRUD(%q, %q): isTODO=%v want %v", tc.path, tc.method, isTODO, tc.wantIsTODO)
		}
		if !tc.wantIsTODO && name != tc.wantName {
			t.Errorf("classifyCRUD(%q, %q): name=%q want %q", tc.path, tc.method, name, tc.wantName)
		}
	}
}

// TestSingularizeTitle exercises the singularisation helper.
func TestSingularizeTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"articles", "Article"},
		{"users", "User"},
		{"profiles", "Profile"},
		{"comments", "Comment"},
		{"tags", "Tag"},
		{"categories", "Category"},
	}
	for _, tc := range cases {
		got := singularizeTitle(tc.in)
		if got != tc.want {
			t.Errorf("singularizeTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFlaskSQLAlchemyDeriver_FKServiceResolution verifies cross-service FK fields
// carry correct RefTable and RefService when a tableServiceMap is provided.
// Uses real Conduit patterns: reference_col('userprofile', ...) (no 's') → profile service.
func TestFlaskSQLAlchemyDeriver_FKServiceResolution(t *testing.T) {
	workspace := writeWorkspaceFiles(t, map[string]string{
		"conduit/articles/models.py": conduitArticleModels,
	})
	cluster := workerdomain.Cluster{
		BlueprintGroup: "conduit.articles",
		Modules:        []workerdomain.Module{"conduit.articles.models"},
	}
	tableMap := map[string]string{
		"userprofile": "profile",  // real tablename (no 's') → profile service
		"article":     "articles", // intra-service
	}

	d := NewFlaskSQLAlchemyDeriver()
	contract, err := d.Derive(context.Background(), cluster, workspace, tableMap)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	var article *workerdomain.ProtoMessage
	for i := range contract.Messages {
		if contract.Messages[i].Name == "Article" {
			article = &contract.Messages[i]
		}
	}
	if article == nil {
		t.Fatal("Article message not found")
	}

	fieldsByName := make(map[string]workerdomain.ProtoField)
	for _, f := range article.Fields {
		fieldsByName[f.Name] = f
	}

	f, ok := fieldsByName["author_identifier"]
	if !ok {
		t.Fatal("Article missing author_identifier field")
	}
	if f.RefTable != "userprofile" {
		t.Errorf("RefTable: got %q, want userprofile (real Conduit, no 's')", f.RefTable)
	}
	if f.RefService != "profile" {
		t.Errorf("RefService: got %q, want profile", f.RefService)
	}
	if !strings.Contains(f.Comment, "references userprofile (service: profile)") {
		t.Errorf("Comment should contain 'references userprofile (service: profile)', got: %q", f.Comment)
	}

	// Without tableServiceMap: RefService must be empty.
	noMapContract, err := d.Derive(context.Background(), cluster, workspace, nil)
	if err != nil {
		t.Fatalf("Derive (no map): %v", err)
	}
	var articleNoMap *workerdomain.ProtoMessage
	for i := range noMapContract.Messages {
		if noMapContract.Messages[i].Name == "Article" {
			articleNoMap = &noMapContract.Messages[i]
		}
	}
	if articleNoMap != nil {
		noMapFields := make(map[string]workerdomain.ProtoField)
		for _, f := range articleNoMap.Fields {
			noMapFields[f.Name] = f
		}
		if f, ok := noMapFields["author_identifier"]; ok {
			if f.RefService != "" {
				t.Errorf("RefService without map should be empty, got %q", f.RefService)
			}
			if strings.Contains(f.Comment, "(service:") {
				t.Errorf("Comment without map should not contain service name, got: %q", f.Comment)
			}
		}
	}
}

// TestAIPFieldName verifies AIP naming rules for both snake_case and camelCase inputs.
func TestAIPFieldName(t *testing.T) {
	cases := []struct{ in, want string }{
		// snake_case inputs (existing rules unchanged).
		{"id", "identifier"},
		{"author_id", "author_identifier"},
		{"article_id", "article_identifier"},
		{"user_id", "user_identifier"},
		{"created_at", "create_time"},
		{"updated_at", "update_time"},
		{"deleted_at", "delete_time"},
		{"title", "title"},
		{"body", "body"},
		// camelCase inputs — must normalise to the same AIP form.
		{"createdAt", "create_time"},
		{"updatedAt", "update_time"},
		{"deletedAt", "delete_time"},
		{"authorId", "author_identifier"},
		{"articleId", "article_identifier"},
		{"userId", "user_identifier"},
	}
	for _, tc := range cases {
		got := aipFieldName(tc.in)
		if got != tc.want {
			t.Errorf("aipFieldName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
