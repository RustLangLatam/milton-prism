<?php
defined('BASEPATH') OR exit('No direct script access allowed');
class Admin extends CI_Controller {
    public function dashboard() {
        $model = 'user_model';
        $this->load->model($model);          // dynamic argument — no edge
        $this->load->model('sub/Foo');       // subfolder load — out of v1, no edge
        $this->load->model('ghost_model');   // nonexistent target — no edge
        $this->load->helper('url');          // helper — out of v1, no edge
    }
}
